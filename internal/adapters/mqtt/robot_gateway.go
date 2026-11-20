package mqtt

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
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
}

func NewRobotGateway(brokerURL string, robots []RobotIdentity, cmds RobotCommands, log *slog.Logger) (*RobotGateway, error) {
	g := &RobotGateway{links: map[string]*robotLink{}, cmds: cmds, log: log}
	for _, r := range robots {
		will, _ := json.Marshal(vda5050.Connection{Header: vda5050.NewHeader(0, time.Now(), r.Manufacturer, r.SerialNumber), ConnectionState: vda5050.ConnectionBroken})
		c, err := Connect(brokerURL, "emu-robot-"+r.SerialNumber, &Will{Topic: vda5050.Topic(r.Manufacturer, r.SerialNumber, "connection"), Payload: will})
		if err != nil {
			g.Close()
			return nil, err
		}
		g.links[r.SerialNumber] = &robotLink{id: r, client: c, headers: map[string]uint32{}}
	}
	return g, nil
}

// Start subscribes to every robot's order and instantActions topics.
func (g *RobotGateway) Start(ctx context.Context) error {
	for serial, l := range g.links {
		orderTopic := vda5050.Topic(l.id.Manufacturer, serial, "order")
		if err := subscribe(l.client, orderTopic, func(payload []byte) { g.onOrder(ctx, serial, payload) }); err != nil {
			return err
		}
		iaTopic := vda5050.Topic(l.id.Manufacturer, serial, "instantActions")
		if err := subscribe(l.client, iaTopic, func(payload []byte) { g.onInstantActions(ctx, serial, payload) }); err != nil {
			return err
		}
	}
	return nil
}

func subscribe(c pahomqtt.Client, topic string, h func(payload []byte)) error {
	tok := c.Subscribe(topic, 1, func(_ pahomqtt.Client, m pahomqtt.Message) { h(m.Payload()) })
	tok.Wait()
	return tok.Error()
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
