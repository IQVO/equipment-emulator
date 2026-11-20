package mqtt

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/IQVO/equipment-emulator/internal/adapters/vda5050"
	"github.com/IQVO/equipment-emulator/internal/domain/robot"
)

type RobotIdentity struct{ Manufacturer, SerialNumber string }

type RobotCommands interface {
	HandleOrder(ctx context.Context, serial string, o robot.Order) error
	HandleInstantActions(ctx context.Context, serial string, as []robot.Action) error
}

type robotLink struct {
	id      RobotIdentity
	client  pahomqtt.Client
	mu      sync.Mutex
	headers map[string]uint32 // per topic
}

func (l *robotLink) header(topic string, at time.Time) vda5050.Header {
	l.mu.Lock()
	defer l.mu.Unlock()
	id := l.headers[topic]
	l.headers[topic] = id + 1
	return vda5050.NewHeader(id, at, l.id.Manufacturer, l.id.SerialNumber)
}

// RobotGateway is one MQTT client per robot, as each real robot would have.
type RobotGateway struct {
	links map[string]*robotLink
	cmds  RobotCommands
	log   *slog.Logger
	ctx   handlerCtx // context for handlers, set by Start
}

func NewRobotGateway(brokerURL string, robots []RobotIdentity, cmds RobotCommands, log *slog.Logger) (*RobotGateway, error) {
	g := &RobotGateway{links: map[string]*robotLink{}, cmds: cmds, log: log}
	g.ctx.set(context.Background())
	for _, r := range robots {
		l := &robotLink{id: r, headers: map[string]uint32{}}
		will, _ := json.Marshal(vda5050.Connection{Header: vda5050.NewHeader(0, time.Now(), r.Manufacturer, r.SerialNumber), ConnectionState: vda5050.ConnectionBroken})
		var connects atomic.Int32
		c, err := Connect(brokerURL, "emu-robot-"+r.SerialNumber, &Will{Topic: vda5050.Topic(r.Manufacturer, r.SerialNumber, "connection"), Payload: will},
			func(c pahomqtt.Client) {
				if err := g.subscribeRobot(c, l); err != nil {
					log.Warn("resubscribe failed", "robot", r.SerialNumber, "err", err)
				}
				if connects.Add(1) > 1 {
					g.reannounce(c, l)
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

// Announcer is implemented by an engine that can republish every robot's
// connection, factsheet and state.
type Announcer interface {
	Announce(ctx context.Context) error
}

// reannounce runs after a reconnect: the broker still holds the retained
// CONNECTION_BROKEN will, so say ONLINE again and republish the factsheet.
func (g *RobotGateway) reannounce(c pahomqtt.Client, l *robotLink) {
	ctx := g.ctx.get()
	topic := vda5050.Topic(l.id.Manufacturer, l.id.SerialNumber, "connection")
	b, err := json.Marshal(vda5050.Connection{Header: l.header(topic, time.Now()), ConnectionState: vda5050.ConnectionOnline})
	if err == nil {
		err = publish(c, topic, true, b)
	}
	if err != nil {
		g.log.Warn("cannot republish ONLINE", "robot", l.id.SerialNumber, "err", err)
	}
	if a, ok := g.cmds.(Announcer); ok {
		if err := a.Announce(ctx); err != nil {
			g.log.Warn("re-announce failed", "robot", l.id.SerialNumber, "err", err)
		}
	}
}

func (g *RobotGateway) subscribeRobot(c pahomqtt.Client, l *robotLink) error {
	serial := l.id.SerialNumber
	ctx := func() context.Context { return g.ctx.get() }
	if err := subscribe(c, vda5050.Topic(l.id.Manufacturer, serial, "order"), func(p []byte) { g.onOrder(ctx(), serial, p) }); err != nil {
		return err
	}
	return subscribe(c, vda5050.Topic(l.id.Manufacturer, serial, "instantActions"), func(p []byte) { g.onInstantActions(ctx(), serial, p) })
}

// Start subscribes to every robot's order and instantActions topics (they are
// subscribed again on every reconnect).
func (g *RobotGateway) Start(ctx context.Context) error {
	g.ctx.set(ctx)
	for _, l := range g.links {
		if err := g.subscribeRobot(l.client, l); err != nil {
			return err
		}
	}
	return nil
}

func (g *RobotGateway) onOrder(ctx context.Context, serial string, payload []byte) {
	var o vda5050.Order
	if err := json.Unmarshal(payload, &o); err != nil {
		g.log.Warn("malformed order", "robot", serial, "err", err)
		return
	}
	d, err := vda5050.ToOrder(o)
	if err != nil {
		g.log.Warn("unsupported order", "robot", serial, "err", err)
		return
	}
	if err := g.cmds.HandleOrder(ctx, serial, d); err != nil {
		g.log.Info("order rejected", "robot", serial, "orderId", o.OrderID, "err", err)
	}
}

func (g *RobotGateway) onInstantActions(ctx context.Context, serial string, payload []byte) {
	var ia vda5050.InstantActions
	if err := json.Unmarshal(payload, &ia); err != nil {
		g.log.Warn("malformed instantActions", "robot", serial, "err", err)
		return
	}
	if err := g.cmds.HandleInstantActions(ctx, serial, vda5050.ToActions(ia.Actions)); err != nil {
		g.log.Info("instant action rejected", "robot", serial, "err", err)
	}
}

func (g *RobotGateway) link(serial string) *robotLink { return g.links[serial] }

func (g *RobotGateway) PublishState(_ context.Context, s robot.State, at time.Time) error {
	l := g.link(s.SerialNumber)
	topic := vda5050.Topic(l.id.Manufacturer, s.SerialNumber, "state")
	b, err := json.Marshal(vda5050.FromState(s, l.header(topic, at)))
	if err != nil {
		return err
	}
	return publish(l.client, topic, false, b)
}

func (g *RobotGateway) PublishConnection(_ context.Context, manufacturer, serial, state string, at time.Time) error {
	l := g.link(serial)
	topic := vda5050.Topic(manufacturer, serial, "connection")
	b, err := json.Marshal(vda5050.Connection{Header: l.header(topic, at), ConnectionState: state})
	if err != nil {
		return err
	}
	return publish(l.client, topic, true, b)
}

func (g *RobotGateway) PublishFactsheet(_ context.Context, f robot.Factsheet, at time.Time) error {
	l := g.link(f.SerialNumber)
	topic := vda5050.Topic(l.id.Manufacturer, f.SerialNumber, "factsheet")
	b, err := json.Marshal(vda5050.FromFactsheet(f, l.header(topic, at)))
	if err != nil {
		return err
	}
	return publish(l.client, topic, true, b)
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
		_ = g.PublishConnection(context.Background(), l.id.Manufacturer, serial, vda5050.ConnectionOffline, time.Now())
		l.client.Disconnect(250)
	}
}
