package mqtt

import (
	"context"
	"errors"
	"log/slog"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"

	"github.com/IQVO/equipment-emulator/internal/adapters/telegram"
	"github.com/IQVO/equipment-emulator/internal/domain/asrs"
)

// ErrNotReady is a transient refusal: the commands cannot run yet (the engine
// is still being built). Its ACK carries the bare reason NOT_READY and is not
// remembered for deduplication, so the sender's retransmission is processed.
var ErrNotReady = errors.New("NOT_READY")

type AsrsCommands interface {
	SubmitJob(asrsID string, j asrs.Job) error
	CancelJob(asrsID, jobID string) error
	UnitStates(ctx context.Context, asrsID string) error
}

// AsrsGateway is the PLC side of one AS/RS.
//
// Two clocks, kept apart on purpose: now is simulated time and stamps every
// telegram it publishes (ACKs included), like every other emulator timestamp;
// wall is real time and drives only the retransmit timer, because the 2 s
// handshake timeout is a property of the wire, not of the simulation.
type AsrsGateway struct {
	asrsID string
	client pahomqtt.Client
	cmds   AsrsCommands
	dedup  *telegram.Dedup
	retx   *telegram.Retransmitter
	now    func() time.Time // simulated time: telegram timestamps
	wall   func() time.Time // real time: the retransmit timer
	log    *slog.Logger
	pub    func(topic string, payload []byte) error
	inbox  *inbox
}

// NewAsrsGateway connects the PLC client of one AS/RS. now is the simulated
// clock that stamps the up telegrams.
func NewAsrsGateway(brokerURL, asrsID string, cmds AsrsCommands, now func() time.Time, log *slog.Logger) (*AsrsGateway, error) {
	g := newAsrsGateway(asrsID, cmds, now, log)
	g.pub = func(topic string, b []byte) error { return publish(g.client, topic, false, b) }
	// A telegram delivered before Start waits in the inbox: the worker starts
	// in Start, after g.client is assigned below, so nothing publishes on a nil client.
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

func newAsrsGateway(asrsID string, cmds AsrsCommands, now func() time.Time, log *slog.Logger) *AsrsGateway {
	return &AsrsGateway{asrsID: asrsID, cmds: cmds, dedup: telegram.NewDedup(1000),
		retx: telegram.NewRetransmitter(2*time.Second, 3), now: now, wall: time.Now, log: log,
		inbox: newInbox(InboxSize, "asrs "+asrsID, log)}
}

func (g *AsrsGateway) subscribeDown(c pahomqtt.Client) error {
	return subscribe(c, telegram.DownTopic(g.asrsID), g.inbox.handler())
}

func pos(p *telegram.Position) asrs.Position {
	if p == nil {
		return asrs.Position{}
	}
	return asrs.Position{Aisle: p.Aisle, Level: p.Level, Slot: p.Slot, Depth: p.Depth}
}

// send publishes an up telegram. A tracked telegram is tracked BEFORE it is
// published: the WCS's ACK can arrive (and be handled) before the publish
// returns, and must find it.
func (g *AsrsGateway) send(t telegram.Telegram, track bool) {
	b, err := telegram.Encode(t)
	if err != nil {
		g.log.Error("cannot encode telegram", "type", t.Type, "err", err)
		return
	}
	if track {
		g.retx.Track(t, g.wall())
	}
	if err := g.pub(telegram.UpTopic(g.asrsID), b); err != nil {
		g.log.Warn("publish failed; the retransmitter will retry", "telegramId", t.ID, "err", err)
	}
}

// handle processes one down telegram. Only the gateway's worker calls it, one
// telegram at a time in arrival order, so no lock is needed: a STORE and a
// later CANCEL of one job are never reordered, and Lookup then Remember of the
// dedup cache cannot interleave.
func (g *AsrsGateway) handle(ctx context.Context, m inbound) {
	reply, afterAck := g.process(ctx, m.payload)
	if reply != nil {
		g.send(*reply, false)
	}
	if afterAck != nil {
		afterAck()
	}
}

// process decodes one down telegram, applies it through the commands and
// returns the telegram to send back (nil for none) plus work to run after
// that reply went out.
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
	switch {
	case errors.Is(cmdErr, ErrNotReady):
		g.log.Warn("not ready; refused without remembering", "type", t.Type, "telegramId", t.ID)
		ack := telegram.NewAck(t, ErrNotReady, uuid.NewString(), g.now())
		return &ack, nil // transient: a retransmission must be processed, not answered from dedup
	case cmdErr != nil && !errors.As(cmdErr, &re):
		g.log.Error("command failed", "type", t.Type, "err", cmdErr)
	}
	ack := telegram.NewAck(t, cmdErr, uuid.NewString(), g.now())
	g.dedup.Remember(t.ID, ack)
	return &ack, afterAck
}

// Start subscribes to the down topic (it is subscribed again on every
// reconnect) and starts the worker that handles down telegrams in arrival
// order until ctx ends.
func (g *AsrsGateway) Start(ctx context.Context) error {
	if err := g.subscribeDown(g.client); err != nil {
		return err
	}
	g.startWorker(ctx)
	return nil
}

func (g *AsrsGateway) startWorker(ctx context.Context) { go g.inbox.drain(ctx, g.handle) }

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
			resend, expired := g.retx.Due(g.wall())
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
