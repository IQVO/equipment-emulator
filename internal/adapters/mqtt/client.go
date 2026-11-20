// Package mqtt connects the emulator to the MQTT broker: the robot side of
// VDA 5050 and the PLC side of the AS/RS telegram protocol.
package mqtt

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

type Will struct {
	Topic   string
	Payload []byte
}

// Connect opens one MQTT client. A will, when given, is published retained by the broker if the client drops.
// onConnect, when given, runs after every (re)connection: the session is clean, so the broker forgets
// subscriptions on a drop and the caller must subscribe again there.
func Connect(brokerURL, clientID string, will *Will, onConnect func(pahomqtt.Client)) (pahomqtt.Client, error) {
	opts := pahomqtt.NewClientOptions().AddBroker(brokerURL).SetClientID(clientID).
		SetAutoReconnect(true).SetConnectRetry(true).SetConnectRetryInterval(2 * time.Second).SetOrderMatters(false)
	if will != nil {
		opts.SetBinaryWill(will.Topic, will.Payload, 1, true)
	}
	if onConnect != nil {
		opts.SetOnConnectHandler(onConnect)
	}
	c := pahomqtt.NewClient(opts)
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
func subscribe(c pahomqtt.Client, topic string, h func(payload []byte)) error {
	tok := c.Subscribe(topic, 1, func(_ pahomqtt.Client, m pahomqtt.Message) { h(m.Payload()) })
	tok.Wait()
	return tok.Error()
}

// handlerCtx holds the context message handlers run with; a handler can fire
// before Start sets it, so it defaults to the background context.
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
