package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/IQVO/equipment-emulator/internal/adapters/vda5050"
	"github.com/IQVO/equipment-emulator/internal/application/engine"
	"github.com/IQVO/equipment-emulator/internal/domain/robot"
)

type RobotIdentity struct{ Manufacturer, SerialNumber string }

type RobotCommands interface {
	HandleOrder(ctx context.Context, serial string, o robot.Order) error
	HandleInstantActions(ctx context.Context, serial string, as []robot.Action) error
	// AnnounceRobot republishes one robot's ONLINE, factsheet and state, unless
	// the robot is fault-disconnected.
	AnnounceRobot(ctx context.Context, serial string) error
}

type robotLink struct {
	id      RobotIdentity
	client  pahomqtt.Client
	mu      sync.Mutex
	headers map[string]uint32 // per topic
	inbox   *inbox            // order and instantActions, in arrival order across both topics
}

func newRobotLink(id RobotIdentity, log *slog.Logger) *robotLink {
	return &robotLink{id: id, headers: map[string]uint32{}, inbox: newInbox(InboxSize, "robot "+id.SerialNumber, log)}
}

func (l *robotLink) header(topic string, at time.Time) vda5050.Header {
	l.mu.Lock()
	defer l.mu.Unlock()
	id := l.headers[topic]
	l.headers[topic] = id + 1
	return vda5050.NewHeader(id, at, l.id.Manufacturer, l.id.SerialNumber)
}

// RobotGateway is one MQTT client per robot, as each real robot would have.
// Every timestamp it publishes is simulated time (now).
type RobotGateway struct {
	links map[string]*robotLink
	cmds  RobotCommands
	now   func() time.Time // simulated time
	log   *slog.Logger
	ctx   handlerCtx // context for the reconnect handler, set by Start
	m     Metrics    // nil records nothing; see SetMetrics
}

// SetMetrics installs the counters the gateway records (ADR 0005). Call it
// before Start: the workers read it without a lock.
func (g *RobotGateway) SetMetrics(m Metrics) { g.m = m }

// NewRobotGateway connects one client per robot. now is the simulated clock;
// the static last will is built here, so it carries the process-start
// (simulation start) timestamp.
func NewRobotGateway(brokerURL string, robots []RobotIdentity, cmds RobotCommands, now func() time.Time, log *slog.Logger) (*RobotGateway, error) {
	g := &RobotGateway{links: map[string]*robotLink{}, cmds: cmds, now: now, log: log}
	g.ctx.set(context.Background())
	for _, r := range robots {
		l := newRobotLink(r, log)
		var connects atomic.Int32
		c, err := Connect(brokerURL, "emu-robot-"+r.SerialNumber, connectionWill(r, now()),
			func(c pahomqtt.Client) {
				if err := g.subscribeRobot(c, l); err != nil {
					log.Warn("resubscribe failed", "robot", r.SerialNumber, "err", err)
				}
				if connects.Add(1) > 1 {
					g.reannounce(l)
				}
			})
		if err != nil {
			g.Close()
			return nil, err
		}
		l.client = c
		g.links[r.SerialNumber] = l
	}
	return g, nil
}

// connectionWill is the retained CONNECTION_BROKEN last will. The broker
// stores it at connect time, so its timestamp is fixed: at, the process start.
func connectionWill(r RobotIdentity, at time.Time) *Will {
	b, _ := json.Marshal(vda5050.Connection{Header: vda5050.NewHeader(0, at, r.Manufacturer, r.SerialNumber), ConnectionState: vda5050.ConnectionBroken})
	return &Will{Topic: vda5050.Topic(r.Manufacturer, r.SerialNumber, "connection"), Payload: b}
}

// reannounce runs after a reconnect: the broker still holds the retained
// CONNECTION_BROKEN will, so the robot says ONLINE again and republishes its
// factsheet and state. It goes through the engine so that a fault-disconnected
// robot stays silent (the engine's guard applies).
func (g *RobotGateway) reannounce(l *robotLink) {
	if err := g.cmds.AnnounceRobot(g.ctx.get(), l.id.SerialNumber); err != nil {
		g.log.Warn("re-announce failed", "robot", l.id.SerialNumber, "err", err)
	}
}

func (g *RobotGateway) subscribeRobot(c pahomqtt.Client, l *robotLink) error {
	if err := subscribe(c, vda5050.Topic(l.id.Manufacturer, l.id.SerialNumber, "order"), l.inbox.handler()); err != nil {
		return err
	}
	return subscribe(c, vda5050.Topic(l.id.Manufacturer, l.id.SerialNumber, "instantActions"), l.inbox.handler())
}

// Start subscribes to every robot's order and instantActions topics (they are
// subscribed again on every reconnect) and starts one worker per robot that
// handles that robot's messages in arrival order until ctx ends.
func (g *RobotGateway) Start(ctx context.Context) error {
	g.ctx.set(ctx)
	for _, l := range g.links {
		if err := g.subscribeRobot(l.client, l); err != nil {
			return err
		}
	}
	for _, l := range g.links {
		g.startWorker(ctx, l)
	}
	return nil
}

func (g *RobotGateway) startWorker(ctx context.Context, l *robotLink) {
	go l.inbox.drain(ctx, func(ctx context.Context, m inbound) { g.dispatch(ctx, l, m) })
}

// dispatch hands one received message to the order or instantActions
// handler, inside a consumer span (the root of its trace: the payload carries
// no trace context) that the engine's resulting publishes are children of.
func (g *RobotGateway) dispatch(ctx context.Context, l *robotLink, m inbound) {
	serial := l.id.SerialNumber
	kind := "unexpected"
	switch m.topic {
	case vda5050.Topic(l.id.Manufacturer, serial, "order"):
		kind = "order"
	case vda5050.Topic(l.id.Manufacturer, serial, "instantActions"):
		kind = "instantActions"
	}
	ctx, span := startSpan(ctx, "receive vda5050 "+kind, trace.SpanKindConsumer, m.topic,
		attrSerial.String(serial), attrMessageType.String(kind))
	var err error
	switch kind {
	case "order":
		err = g.onOrder(ctx, serial, m.payload)
	case "instantActions":
		err = g.onInstantActions(ctx, serial, m.payload)
	default:
		g.log.WarnContext(ctx, "message on an unexpected topic", "robot", serial, "topic", m.topic)
	}
	endSpan(span, err)
}

// onOrder applies one order and counts its outcome. It returns an error only
// for a message the emulator could not handle (malformed, unsupported); a
// rejection by the robot is a normal outcome, recorded on the span.
func (g *RobotGateway) onOrder(ctx context.Context, serial string, payload []byte) error {
	m := metricsOrNoop(g.m)
	var o vda5050.Order
	if err := json.Unmarshal(payload, &o); err != nil {
		g.log.WarnContext(ctx, "malformed order", "robot", serial, "err", err)
		m.OrderReceived(ctx, outcomeRejected, reasonMalformed)
		return err
	}
	d, err := vda5050.ToOrder(o)
	if err != nil {
		g.log.WarnContext(ctx, "unsupported order", "robot", serial, "err", err)
		m.OrderReceived(ctx, outcomeRejected, reasonUnsupported)
		return err
	}
	err = g.cmds.HandleOrder(ctx, serial, d)
	outcome, reason := orderOutcome(err)
	trace.SpanFromContext(ctx).SetAttributes(attribute.String("vda5050.order.id", o.OrderID),
		attribute.String("outcome", outcome), attribute.String("reason", reason))
	m.OrderReceived(ctx, outcome, reason)
	if err != nil {
		g.log.InfoContext(ctx, "order rejected", "robot", serial, "orderId", o.OrderID, "err", err)
	}
	return nil
}

// orderOutcome classifies HandleOrder's error. The robot's own refusal names
// its VDA 5050 error type; a command dropped before the robot saw it names
// why. Any other error can only come from publishing the resulting state,
// after the order was accepted.
func orderOutcome(err error) (outcome, reason string) {
	var re *robot.RejectError
	switch {
	case err == nil:
		return outcomeAccepted, ""
	case errors.As(err, &re):
		return outcomeRejected, re.Type
	case errors.Is(err, engine.ErrRobotDisconnected):
		return outcomeRejected, "ROBOT_DISCONNECTED"
	case errors.Is(err, engine.ErrUnknownRobot):
		return outcomeRejected, "UNKNOWN_ROBOT"
	case errors.Is(err, ErrNotReady):
		return outcomeRejected, ErrNotReady.Error()
	default:
		return outcomeAccepted, ""
	}
}

func (g *RobotGateway) onInstantActions(ctx context.Context, serial string, payload []byte) error {
	var ia vda5050.InstantActions
	if err := json.Unmarshal(payload, &ia); err != nil {
		g.log.WarnContext(ctx, "malformed instantActions", "robot", serial, "err", err)
		return err
	}
	if err := g.cmds.HandleInstantActions(ctx, serial, vda5050.ToActions(ia.Actions)); err != nil {
		g.log.InfoContext(ctx, "instant action rejected", "robot", serial, "err", err)
	}
	return nil
}

func (g *RobotGateway) link(serial string) *robotLink { return g.links[serial] }

// send publishes one VDA 5050 message inside a producer span, a child of the
// span in ctx (the order receive, the admin request or none for a tick).
func (g *RobotGateway) send(ctx context.Context, l *robotLink, kind, topic string, retained bool, msg any) error {
	_, span := startSpan(ctx, "publish vda5050 "+kind, trace.SpanKindProducer, topic,
		attrSerial.String(l.id.SerialNumber), attrMessageType.String(kind))
	b, err := json.Marshal(msg)
	if err == nil {
		err = publish(l.client, topic, retained, b)
	}
	endSpan(span, err)
	return err
}

func (g *RobotGateway) PublishState(ctx context.Context, s robot.State, at time.Time) error {
	l := g.link(s.SerialNumber)
	topic := vda5050.Topic(l.id.Manufacturer, s.SerialNumber, "state")
	return g.send(ctx, l, "state", topic, false, vda5050.FromState(s, l.header(topic, at)))
}

func (g *RobotGateway) PublishConnection(ctx context.Context, manufacturer, serial, state string, at time.Time) error {
	l := g.link(serial)
	topic := vda5050.Topic(manufacturer, serial, "connection")
	return g.send(ctx, l, "connection", topic, true, vda5050.Connection{Header: l.header(topic, at), ConnectionState: state})
}

func (g *RobotGateway) PublishFactsheet(ctx context.Context, f robot.Factsheet, at time.Time) error {
	l := g.link(f.SerialNumber)
	topic := vda5050.Topic(l.id.Manufacturer, f.SerialNumber, "factsheet")
	return g.send(ctx, l, "factsheet", topic, true, vda5050.FromFactsheet(f, l.header(topic, at)))
}

func (g *RobotGateway) Connected() bool {
	for _, l := range g.links {
		if !l.client.IsConnectionOpen() {
			return false
		}
	}
	return true
}

// Close publishes OFFLINE for every robot (a coordinated shutdown) and disconnects.
func (g *RobotGateway) Close() {
	for serial, l := range g.links {
		_ = g.PublishConnection(context.Background(), l.id.Manufacturer, serial, vda5050.ConnectionOffline, g.now())
		l.client.Disconnect(250)
	}
}
