// Package mqtt connects the emulator to the MQTT broker: the robot side of
// VDA 5050 and the PLC side of the AS/RS telegram protocol.
package mqtt

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

type Will struct {
	Topic   string
	Payload []byte
}

// clientOptions are the options of every emulator client. OrderMatters is on:
// paho then calls the message handlers one at a time, in arrival order, on its
// own goroutine, so a handler must never block (it only enqueues, see inbox).
func clientOptions(brokerURL, clientID string, will *Will, onConnect func(pahomqtt.Client)) *pahomqtt.ClientOptions {
	opts := pahomqtt.NewClientOptions().AddBroker(brokerURL).SetClientID(clientID).
		SetAutoReconnect(true).SetConnectRetry(true).SetConnectRetryInterval(2 * time.Second).SetOrderMatters(true)
	if will != nil {
		opts.SetBinaryWill(will.Topic, will.Payload, 1, true)
	}
	if onConnect != nil {
		opts.SetOnConnectHandler(onConnect)
	}
	return opts
}

// Connect opens one MQTT client. A will, when given, is published retained by the broker if the client drops.
// onConnect, when given, runs after every (re)connection: the session is clean, so the broker forgets
// subscriptions on a drop and the caller must subscribe again there.
func Connect(brokerURL, clientID string, will *Will, onConnect func(pahomqtt.Client)) (pahomqtt.Client, error) {
	c := pahomqtt.NewClient(clientOptions(brokerURL, clientID, will, onConnect))
	tok := c.Connect()
	if !tok.WaitTimeout(15*time.Second) || tok.Error() != nil {
		c.Disconnect(0) // stop the background connect retries
		return nil, fmt.Errorf("mqtt connect %s as %s: %v", brokerURL, clientID, tok.Error())
	}
	return c, nil
}

func publish(c pahomqtt.Client, topic string, retained bool, payload []byte) error {
	tok := c.Publish(topic, 1, retained, payload)
	if !tok.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("mqtt publish %s: timeout", topic)
	}
	return tok.Error()
}

// subscribe subscribes and waits for the broker's answer.
func subscribe(c pahomqtt.Client, topic string, h pahomqtt.MessageHandler) error {
	tok := c.Subscribe(topic, 1, h)
	tok.Wait()
	return tok.Error()
}

// InboxSize is how many received messages one link (a robot, an AS/RS) buffers
// while its worker is busy or not yet started.
const InboxSize = 1024

// inbound is one received message.
type inbound struct {
	topic   string
	payload []byte
}

// inbox is the per-link queue between paho and the link's single worker. The
// paho handler only enqueues, without blocking, so paho's ordered delivery is
// never stalled; the worker then handles messages one at a time in arrival
// order (a STORE before its CANCEL, order update n before n+1).
//
// When the buffer is full the message is logged and dropped. That is an
// emulator's choice: QoS 1 was already acknowledged to the broker, a down
// telegram is retransmitted by the WCS after 2 s, and a lost VDA 5050 order
// shows up as an unchanged state. With 1024 slots it means the worker is stuck.
type inbox struct {
	ch   chan inbound
	log  *slog.Logger
	link string
}

func newInbox(size int, link string, log *slog.Logger) *inbox {
	return &inbox{ch: make(chan inbound, size), log: log, link: link}
}

// handler is the paho message handler: it enqueues and returns at once.
func (b *inbox) handler() pahomqtt.MessageHandler {
	return func(_ pahomqtt.Client, m pahomqtt.Message) { b.offer(inbound{topic: m.Topic(), payload: m.Payload()}) }
}

func (b *inbox) offer(m inbound) {
	select {
	case b.ch <- m:
	default:
		b.log.Error("inbox full; message dropped", "link", b.link, "topic", m.topic, "capacity", cap(b.ch))
	}
}

// drain handles queued messages in arrival order until ctx ends.
func (b *inbox) drain(ctx context.Context, handle func(context.Context, inbound)) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-b.ch:
			handle(ctx, m)
		}
	}
}

// handlerCtx holds the context the reconnect handler runs with; a reconnect
// can happen before Start sets it, so it defaults to the background context.
type handlerCtx struct {
	p atomic.Pointer[context.Context]
}

func (h *handlerCtx) set(ctx context.Context) { h.p.Store(&ctx) }
func (h *handlerCtx) get() context.Context {
	if p := h.p.Load(); p != nil {
		return *p
	}
	return context.Background()
}
