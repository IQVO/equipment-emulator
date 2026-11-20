package mqtt

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/IQVO/equipment-emulator/internal/adapters/telegram"
	"github.com/IQVO/equipment-emulator/internal/domain/asrs"
)

// fakeAsrs queues and cancels like the PLC: a CANCEL of a job it has not
// queued is refused with UNKNOWN_JOB.
type fakeAsrs struct {
	mu      sync.Mutex
	submits int
	queued  map[string]bool
	errs    []error // returned by successive SubmitJob calls, then nil
	delay   time.Duration
}

func (f *fakeAsrs) SubmitJob(_ string, j asrs.Job) error {
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submits++
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		if err != nil {
			return err
		}
	}
	if f.queued == nil {
		f.queued = map[string]bool{}
	}
	f.queued[j.ID] = true
	return nil
}

func (f *fakeAsrs) CancelJob(_, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.queued[id] {
		return &asrs.RejectError{Reason: asrs.ReasonUnknownJob}
	}
	delete(f.queued, id)
	return nil
}

func (f *fakeAsrs) UnitStates(context.Context, string) error { return nil }

// sink records what a gateway publishes.
type sink struct {
	mu   sync.Mutex
	sent []telegram.Telegram
}

func (s *sink) pub(_ string, b []byte) error {
	t, err := telegram.Decode(b)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.sent = append(s.sent, t)
	s.mu.Unlock()
	return nil
}

func (s *sink) snapshot() []telegram.Telegram {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]telegram.Telegram{}, s.sent...)
}

// waitFor polls until n telegrams were published.
func (s *sink) waitFor(t *testing.T, n int) []telegram.Telegram {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := s.snapshot(); len(got) >= n {
			return got
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("published %d telegrams, want %d", len(s.snapshot()), n)
	return nil
}

var simStart = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

func testGateway(cmds AsrsCommands) (*AsrsGateway, *sink) {
	out := &sink{}
	g := newAsrsGateway("A1", cmds, func() time.Time { return simStart }, quietLog())
	g.pub = out.pub
	return g, out
}

// fakeMessage is what paho hands a message handler.
type fakeMessage struct {
	topic   string
	payload []byte
}

func (m fakeMessage) Duplicate() bool   { return false }
func (m fakeMessage) Qos() byte         { return 1 }
func (m fakeMessage) Retained() bool    { return false }
func (m fakeMessage) Topic() string     { return m.topic }
func (m fakeMessage) MessageID() uint16 { return 0 }
func (m fakeMessage) Payload() []byte   { return m.payload }
func (m fakeMessage) Ack()              {}

// deliver calls a paho message handler the way paho does with OrderMatters on: one message at a time.
func deliver(h pahomqtt.MessageHandler, topic string, payloads ...string) {
	for _, p := range payloads {
		h(nil, fakeMessage{topic: topic, payload: []byte(p)})
	}
}

const storeJSON = `{"telegramId":"s1","type":"STORE","timestamp":"2026-11-19T08:00:00.000Z","jobId":"j1","containerId":"C1","portId":"IN1","to":{"aisle":1,"level":1,"slot":1,"depth":1}}`
const cancelJSON = `{"telegramId":"c1","type":"CANCEL","timestamp":"2026-11-19T08:00:00.100Z","jobId":"j1"}`

func ackOf(id string) string {
	return `{"telegramId":"ack-` + id + `","type":"ACK","timestamp":"2026-11-19T08:00:00.000Z","ackTelegramId":"` + id + `","accepted":true}`
}

func TestMalformedTelegramNackIsRememberedAndResent(t *testing.T) {
	g, _ := testGateway(&fakeAsrs{})
	bad := []byte(`{"telegramId":"m1","type":"BOGUS","timestamp":"2026-11-19T08:00:00.000Z"}`)
	first, _ := g.process(context.Background(), bad)
	second, _ := g.process(context.Background(), bad)
	if first == nil || second == nil {
		t.Fatal("both deliveries must be answered")
	}
	if first.ID != second.ID || first.AckOf != "m1" {
		t.Fatalf("retransmit got a new NACK: %q vs %q", first.ID, second.ID)
	}
}

func TestDuplicateStoreSubmitsOnceAndRepeatsAck(t *testing.T) {
	f := &fakeAsrs{}
	g, _ := testGateway(f)
	first, _ := g.process(context.Background(), []byte(storeJSON))
	second, _ := g.process(context.Background(), []byte(storeJSON))
	if f.submits != 1 {
		t.Fatalf("SubmitJob called %d times", f.submits)
	}
	if first.ID != second.ID || first.Accepted == nil || !*first.Accepted {
		t.Fatalf("expected the same accepted ACK twice: %+v %+v", first, second)
	}
}

func TestClientOptionsKeepArrivalOrder(t *testing.T) {
	if !clientOptions("tcp://x:1883", "c", nil, nil).Order {
		t.Fatal("every client must set OrderMatters(true)")
	}
}

func TestStoreThenCancelThroughTheCallbackAreHandledInOrder(t *testing.T) {
	f := &fakeAsrs{delay: 5 * time.Millisecond} // a slow STORE must still precede its CANCEL
	g, out := testGateway(f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g.startWorker(ctx)
	deliver(g.inbox.handler(), telegram.DownTopic("A1"), storeJSON, cancelJSON)
	sent := out.waitFor(t, 2)
	if sent[0].AckOf != "s1" || sent[1].AckOf != "c1" {
		t.Fatalf("ACK order = %s, %s", sent[0].AckOf, sent[1].AckOf)
	}
	if !*sent[1].Accepted {
		t.Fatalf("the CANCEL ran before its STORE: reason %s", sent[1].Reason)
	}
}

func TestDownTelegramsWaitInTheInboxUntilTheWorkerStarts(t *testing.T) {
	g, out := testGateway(&fakeAsrs{})
	deliver(g.inbox.handler(), telegram.DownTopic("A1"), storeJSON) // returns at once: nothing handles it yet
	time.Sleep(20 * time.Millisecond)
	if n := len(out.snapshot()); n != 0 {
		t.Fatalf("handled before the worker started (the client may still be nil): %d", n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g.startWorker(ctx)
	if sent := out.waitFor(t, 1); sent[0].AckOf != "s1" {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestFullInboxDropsWithoutBlocking(t *testing.T) {
	g, _ := testGateway(&fakeAsrs{})
	g.inbox = newInbox(2, "asrs A1", quietLog())
	done := make(chan struct{})
	go func() {
		deliver(g.inbox.handler(), telegram.DownTopic("A1"), storeJSON, storeJSON, storeJSON, storeJSON)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the paho handler blocked on a full inbox")
	}
	if n := len(g.inbox.ch); n != 2 {
		t.Fatalf("queued %d, want 2 (the rest dropped)", n)
	}
}

func TestWorkerStopsWhenContextEnds(t *testing.T) {
	g, out := testGateway(&fakeAsrs{})
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { g.inbox.drain(ctx, g.handle); close(stopped) }()
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	deliver(g.inbox.handler(), telegram.DownTopic("A1"), storeJSON)
	time.Sleep(10 * time.Millisecond)
	if n := len(out.snapshot()); n != 0 {
		t.Fatalf("handled after stop: %d", n)
	}
}

func TestUpTelegramIsTrackedBeforeItIsPublished(t *testing.T) {
	g, out := testGateway(&fakeAsrs{})
	// the WCS ACKs so fast that its ACK is handled before publish returns
	g.pub = func(topic string, b []byte) error {
		if err := out.pub(topic, b); err != nil {
			return err
		}
		var up struct {
			ID string `json:"telegramId"`
		}
		_ = json.Unmarshal(b, &up)
		g.process(context.Background(), []byte(ackOf(up.ID)))
		return nil
	}
	if err := g.Publish(context.Background(), []asrs.Event{{Kind: asrs.EventJobDone, JobID: "j1"}}, simStart); err != nil {
		t.Fatal(err)
	}
	if resend, _ := g.retx.Due(time.Now().Add(time.Hour)); len(resend) != 0 {
		t.Fatalf("an acknowledged telegram is retransmitted: %+v", resend)
	}
}

func TestTimestampsAreSimulatedButTheRetransmitTimerIsReal(t *testing.T) {
	g, out := testGateway(&fakeAsrs{})
	ack, _ := g.process(context.Background(), []byte(storeJSON))
	if !ack.Timestamp.Equal(simStart) {
		t.Fatalf("ACK timestamp %s, want the simulated %s", ack.Timestamp, simStart)
	}
	if err := g.Publish(context.Background(), []asrs.Event{{Kind: asrs.EventJobDone, JobID: "j1"}}, simStart); err != nil {
		t.Fatal(err)
	}
	if sent := out.snapshot(); !sent[0].Timestamp.Equal(simStart) {
		t.Fatalf("JOB_DONE timestamp %s", sent[0].Timestamp)
	}
	// simulated time is years ahead of wall time: a sim-clocked timer would never be due now
	if resend, _ := g.retx.Due(time.Now().Add(3 * time.Second)); len(resend) != 1 {
		t.Fatalf("the retransmit timer must run on wall time: %d due", len(resend))
	}
}
