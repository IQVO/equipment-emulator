package mqtt

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"

	"github.com/IQVO/equipment-emulator/internal/adapters/telegram"
	"github.com/IQVO/equipment-emulator/internal/domain/asrs"
)

type AsrsCommands interface {
	SubmitJob(asrsID string, j asrs.Job) error
	CancelJob(asrsID, jobID string) error
	UnitStates(ctx context.Context, asrsID string) error
}

// AsrsGateway is the PLC side of one AS/RS.
type AsrsGateway struct {
	asrsID string
	client pahomqtt.Client
	cmds   AsrsCommands
	dedup  *telegram.Dedup
	retx   *telegram.Retransmitter
	now    func() time.Time
	log    *slog.Logger
	mu     sync.Mutex // serialises handle
	pub    func(topic string, payload []byte) error
	ctx    handlerCtx // context for handlers, set by Start
}

func NewAsrsGateway(brokerURL, asrsID string, cmds AsrsCommands, now func() time.Time, log *slog.Logger) (*AsrsGateway, error) {
	g := &AsrsGateway{asrsID: asrsID, cmds: cmds, dedup: telegram.NewDedup(1000),
		retx: telegram.NewRetransmitter(2*time.Second, 3), now: now, log: log}
	g.ctx.set(context.Background())
	g.pub = func(topic string, b []byte) error { return publish(g.client, topic, false, b) }
	c, err := Connect(brokerURL, "emu-asrs-"+asrsID, nil, func(c pahomqtt.Client) {
		if err := g.subscribeDown(c); err != nil {
			log.Warn("resubscribe failed", "asrs", asrsID, "err", err)
		}
	})
	if err != nil {
		return nil, err
	}
	g.client = c
	return g, nil
}

func (g *AsrsGateway) subscribeDown(c pahomqtt.Client) error {
	return subscribe(c, telegram.DownTopic(g.asrsID), func(p []byte) { g.handle(g.ctx.get(), p) })
}

func pos(p *telegram.Position) asrs.Position {
	if p == nil {
		return asrs.Position{}
	}
	return asrs.Position{Aisle: p.Aisle, Level: p.Level, Slot: p.Slot, Depth: p.Depth}
}

func (g *AsrsGateway) send(t telegram.Telegram, track bool) {
	b, err := telegram.Encode(t)
	if err != nil {
		g.log.Error("cannot encode telegram", "type", t.Type, "err", err)
		return
	}
	if err := g.pub(telegram.UpTopic(g.asrsID), b); err != nil {
		g.log.Warn("publish failed; the retransmitter will retry", "telegramId", t.ID, "err", err)
	}
	if track {
		g.retx.Track(t, g.now())
	}
}

// handle serialises the processing of down telegrams: the client delivers
// messages concurrently (OrderMatters is off), but a STORE and a later CANCEL
// of one job must not be reordered, and Lookup then Remember must be atomic.
func (g *AsrsGateway) handle(ctx context.Context, payload []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	reply, afterAck := g.process(ctx, payload)
	if reply != nil {
		g.send(*reply, false)
	}
	if afterAck != nil {
		afterAck()
	}
}

// process decodes one down telegram, applies it through the commands and
// returns the telegram to send back (nil for none) plus work to run after
// that reply went out. The caller must hold g.mu.
func (g *AsrsGateway) process(ctx context.Context, payload []byte) (reply *telegram.Telegram, afterAck func()) {
	t, err := telegram.Decode(payload)
	if err != nil {
		g.log.Warn("malformed telegram", "err", err)
		if t.ID == "" {
			return nil, nil
		}
		if nack, seen := g.dedup.Lookup(t.ID); seen {
			return &nack, nil
		}
		nack := telegram.NewAck(t, errors.New("MALFORMED"), uuid.NewString(), g.now())
		g.dedup.Remember(t.ID, nack)
		return &nack, nil
	}
	if t.Type == telegram.Ack {
		g.retx.Ack(t.AckOf)
		return nil, nil
	}
	if ack, seen := g.dedup.Lookup(t.ID); seen {
		return &ack, nil
	}
	var cmdErr error
	switch t.Type {
	case telegram.Store:
		cmdErr = g.cmds.SubmitJob(g.asrsID, asrs.Job{ID: t.JobID, Kind: asrs.KindStore, ContainerID: t.ContainerID, Port: t.PortID, To: pos(t.To)})
	case telegram.Retrieve:
		cmdErr = g.cmds.SubmitJob(g.asrsID, asrs.Job{ID: t.JobID, Kind: asrs.KindRetrieve, ContainerID: t.ContainerID, Port: t.PortID, From: pos(t.From)})
	case telegram.Move:
		cmdErr = g.cmds.SubmitJob(g.asrsID, asrs.Job{ID: t.JobID, Kind: asrs.KindMove, ContainerID: t.ContainerID, From: pos(t.From), To: pos(t.To)})
	case telegram.Cancel:
		cmdErr = g.cmds.CancelJob(g.asrsID, t.JobID)
	case telegram.StateRequest:
		afterAck = func() { _ = g.cmds.UnitStates(ctx, g.asrsID) }
	default:
		cmdErr = errors.New("MALFORMED")
	}
	var re *asrs.RejectError
	if cmdErr != nil && !errors.As(cmdErr, &re) {
		g.log.Error("command failed", "type", t.Type, "err", cmdErr)
	}
	ack := telegram.NewAck(t, cmdErr, uuid.NewString(), g.now())
	g.dedup.Remember(t.ID, ack)
	return &ack, afterAck
}

// Start subscribes to the down topic (and again on every reconnect).
func (g *AsrsGateway) Start(ctx context.Context) error {
	g.ctx.set(ctx)
	return g.subscribeDown(g.client)
}

// Publish turns PLC events into up telegrams that wait for an ACK.
func (g *AsrsGateway) Publish(_ context.Context, evs []asrs.Event, at time.Time) error {
	for _, e := range evs {
		t := telegram.Telegram{ID: uuid.NewString(), Timestamp: at}
		switch e.Kind {
		case asrs.EventJobDone:
			t.Type, t.JobID = telegram.JobDone, e.JobID
		case asrs.EventJobFailed:
			t.Type, t.JobID, t.Reason = telegram.JobFailed, e.JobID, e.Reason
		case asrs.EventUnitState:
			t.Type, t.UnitID, t.State = telegram.UnitState, e.UnitID, string(e.State)
		case asrs.EventPortArrival:
			t.Type, t.PortID, t.ContainerID = telegram.PortArrival, e.PortID, e.ContainerID
		}
		g.send(t, true)
	}
	return nil
}

// RunRetransmit resends unacknowledged up telegrams until ctx ends.
func (g *AsrsGateway) RunRetransmit(ctx context.Context) {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			resend, expired := g.retx.Due(g.now())
			for _, t := range resend {
				g.send(t, false)
			}
			for _, t := range expired {
				g.log.Warn("telegram never acknowledged", "telegramId", t.ID, "type", t.Type)
			}
		}
	}
}

func (g *AsrsGateway) Connected() bool { return g.client.IsConnectionOpen() }
func (g *AsrsGateway) Close()          { g.client.Disconnect(250) }

// AsrsRouter routes engine events to the gateway of each AS/RS.
type AsrsRouter map[string]*AsrsGateway

func (r AsrsRouter) PublishEvents(ctx context.Context, asrsID string, evs []asrs.Event, at time.Time) error {
	g, ok := r[asrsID]
	if !ok {
		return errors.New("no gateway for asrs " + asrsID)
	}
	return g.Publish(ctx, evs, at)
}
