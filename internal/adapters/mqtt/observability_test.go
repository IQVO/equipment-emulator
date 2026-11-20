package mqtt

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/IQVO/equipment-emulator/internal/adapters/telegram"
	"github.com/IQVO/equipment-emulator/internal/adapters/vda5050"
	"github.com/IQVO/equipment-emulator/internal/application/engine"
	"github.com/IQVO/equipment-emulator/internal/domain/asrs"
	"github.com/IQVO/equipment-emulator/internal/domain/robot"
)

// recordSpans installs a TracerProvider with an in-memory recorder for the test.
func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	return rec
}

// fakeMetrics records every Metrics call as a string.
type fakeMetrics struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeMetrics) add(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeMetrics) OrderReceived(_ context.Context, outcome, reason string) {
	f.add("order " + outcome + " " + reason)
}
func (f *fakeMetrics) TelegramSeen(_ context.Context, direction, typ string) {
	f.add("telegram " + direction + " " + typ)
}
func (f *fakeMetrics) TelegramRetransmitted(_ context.Context, typ string) {
	f.add("retransmit " + typ)
}
func (f *fakeMetrics) TelegramExpired(_ context.Context, typ string) { f.add("expired " + typ) }

func (f *fakeMetrics) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.calls...)
}

func attrsOf(s sdktrace.ReadOnlySpan) map[attribute.Key]string {
	out := map[attribute.Key]string{}
	for _, kv := range s.Attributes() {
		out[kv.Key] = kv.Value.String()
	}
	return out
}

func spanNamed(t *testing.T, rec *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	var names []string
	for _, s := range rec.Ended() {
		if s.Name() == name {
			return s
		}
		names = append(names, s.Name())
	}
	t.Fatalf("no span %q; ended spans: %v", name, names)
	return nil
}

func wantAttrs(t *testing.T, s sdktrace.ReadOnlySpan, want map[attribute.Key]string) {
	t.Helper()
	got := attrsOf(s)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("span %q: %s = %q, want %q (all: %v)", s.Name(), k, got[k], v, got)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDownTelegramAndItsAckAreTracedAndCounted(t *testing.T) {
	rec := recordSpans(t)
	m := &fakeMetrics{}
	g, _ := testGateway(&fakeAsrs{})
	g.SetMetrics(m)

	g.handle(context.Background(), inbound{topic: telegram.DownTopic("A1"), payload: []byte(storeJSON)})

	recv := spanNamed(t, rec, "receive telegram STORE")
	if recv.SpanKind() != trace.SpanKindConsumer {
		t.Errorf("receive span kind = %v, want consumer", recv.SpanKind())
	}
	wantAttrs(t, recv, map[attribute.Key]string{
		"messaging.system": "mqtt", "messaging.destination.name": "asrs/v1/A1/down",
		"asrs.id": "A1", "telegram.id": "s1", "telegram.type": "STORE",
	})
	ack := spanNamed(t, rec, "publish telegram ACK")
	if ack.SpanKind() != trace.SpanKindProducer {
		t.Errorf("ack span kind = %v, want producer", ack.SpanKind())
	}
	if ack.Parent().SpanID() != recv.SpanContext().SpanID() {
		t.Error("the ACK publish span is not a child of the receive span")
	}
	wantAttrs(t, ack, map[attribute.Key]string{"asrs.id": "A1", "telegram.type": "ACK", "telegram.retransmit": "false"})
	if got, want := m.snapshot(), []string{"telegram down STORE", "telegram up ACK"}; !equalStrings(got, want) {
		t.Errorf("metrics = %v, want %v", got, want)
	}
}

func TestMalformedTelegramSpanIsAnErrorAndItsTypeIsBounded(t *testing.T) {
	rec := recordSpans(t)
	m := &fakeMetrics{}
	g, _ := testGateway(&fakeAsrs{})
	g.SetMetrics(m)

	g.handle(context.Background(), inbound{payload: []byte(`{"telegramId":"m1","type":"BOGUS","timestamp":"2026-11-19T08:00:00.000Z"}`)})

	recv := spanNamed(t, rec, "receive telegram UNKNOWN")
	if recv.Status().Code != codes.Error {
		t.Errorf("status = %v, want Error", recv.Status())
	}
	wantAttrs(t, recv, map[attribute.Key]string{"telegram.id": "m1", "telegram.type": "UNKNOWN"})
	if got, want := m.snapshot(), []string{"telegram down UNKNOWN", "telegram up ACK"}; !equalStrings(got, want) {
		t.Errorf("metrics = %v, want %v", got, want)
	}
}

func TestUpTelegramRetransmitsAndExpiryAreTracedAndCounted(t *testing.T) {
	rec := recordSpans(t)
	m := &fakeMetrics{}
	g, out := testGateway(&fakeAsrs{})
	g.SetMetrics(m)
	wall := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	g.wall = func() time.Time { return wall }

	if err := g.Publish(context.Background(), []asrs.Event{{Kind: asrs.EventJobDone, JobID: "j1"}}, simStart); err != nil {
		t.Fatal(err)
	}
	for range 4 { // three retransmissions, then the telegram expires
		wall = wall.Add(2100 * time.Millisecond)
		g.retransmitDue(context.Background())
	}

	if n := len(out.snapshot()); n != 4 {
		t.Errorf("published %d telegrams, want the first send and 3 retransmissions", n)
	}
	want := []string{"telegram up JOB_DONE", "retransmit JOB_DONE", "retransmit JOB_DONE", "retransmit JOB_DONE", "expired JOB_DONE"}
	if got := m.snapshot(); !equalStrings(got, want) {
		t.Errorf("metrics = %v, want %v", got, want)
	}
	retransmits := 0
	for _, s := range rec.Ended() {
		if s.Name() == "publish telegram JOB_DONE" && attrsOf(s)["telegram.retransmit"] == "true" {
			retransmits++
		}
	}
	if retransmits != 3 {
		t.Errorf("%d retransmit spans, want 3", retransmits)
	}
}

// fakeClient is the part of a paho client the robot gateway publishes through.
type fakeClient struct {
	pahomqtt.Client
	mu     sync.Mutex
	topics []string
	err    error
}

func (c *fakeClient) Publish(topic string, _ byte, _ bool, _ any) pahomqtt.Token {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.topics = append(c.topics, topic)
	return &doneToken{err: c.err}
}

type doneToken struct{ err error }

func (t *doneToken) Wait() bool                     { return true }
func (t *doneToken) WaitTimeout(time.Duration) bool { return true }
func (t *doneToken) Done() <-chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}
func (t *doneToken) Error() error { return t.err }

func TestOrderOutcomesAreCountedAndTraced(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		err     error
		want    string
	}{
		{"accepted", orderJSON("o1", 0), nil, "order accepted "},
		{"a state publish failure still means accepted", orderJSON("o1", 0), errors.New("mqtt publish: timeout"), "order accepted "},
		{"a domain rejection carries the VDA 5050 error type", orderJSON("o1", 0), &robot.RejectError{Type: robot.ErrInvalidOrder, Reason: "x"}, "order rejected " + robot.ErrInvalidOrder},
		{"a fault-disconnected robot", orderJSON("o1", 0), fmt.Errorf("%w: R1", engine.ErrRobotDisconnected), "order rejected ROBOT_DISCONNECTED"},
		{"the engine is not ready", orderJSON("o1", 0), ErrNotReady, "order rejected NOT_READY"},
		{"malformed", `{"orderId":`, nil, "order rejected malformed"},
		{"unsupported", `{"orderId":"o1","nodes":[{"nodeId":"n1","released":true}]}`, nil, "order rejected unsupported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := recordSpans(t)
			m := &fakeMetrics{}
			g := &RobotGateway{links: map[string]*robotLink{}, cmds: &fakeRobots{err: tc.err}, now: time.Now, log: quietLog()}
			g.SetMetrics(m)
			l := newRobotLink(RobotIdentity{"EMU", "R1"}, quietLog())

			g.dispatch(context.Background(), l, inbound{topic: vda5050.Topic("EMU", "R1", "order"), payload: []byte(tc.payload)})

			if got := m.snapshot(); len(got) != 1 || got[0] != tc.want {
				t.Errorf("metrics = %v, want [%s]", got, tc.want)
			}
			s := spanNamed(t, rec, "receive vda5050 order")
			if s.SpanKind() != trace.SpanKindConsumer {
				t.Errorf("kind = %v, want consumer", s.SpanKind())
			}
			wantAttrs(t, s, map[attribute.Key]string{
				"messaging.system": "mqtt", "messaging.destination.name": "vda5050/v3/EMU/R1/order",
				"vda5050.serial_number": "R1", "vda5050.message.type": "order",
			})
		})
	}
}

func TestRobotPublishesAreProducerSpansUnderTheCallersContext(t *testing.T) {
	rec := recordSpans(t)
	client := &fakeClient{}
	l := newRobotLink(RobotIdentity{"EMU", "R1"}, quietLog())
	l.client = client
	g := &RobotGateway{links: map[string]*robotLink{"R1": l}, now: time.Now, log: quietLog()}

	ctx, parent := otel.Tracer("test").Start(context.Background(), "parent")
	if err := g.PublishState(ctx, robot.State{SerialNumber: "R1"}, simStart); err != nil {
		t.Fatal(err)
	}
	if err := g.PublishConnection(ctx, "EMU", "R1", vda5050.ConnectionOnline, simStart); err != nil {
		t.Fatal(err)
	}
	client.err = errors.New("broker down")
	if err := g.PublishFactsheet(ctx, robot.Factsheet{SerialNumber: "R1"}, simStart); err == nil {
		t.Fatal("a failed publish must return its error")
	}
	parent.End()

	for _, typ := range []string{"state", "connection", "factsheet"} {
		s := spanNamed(t, rec, "publish vda5050 "+typ)
		if s.SpanKind() != trace.SpanKindProducer {
			t.Errorf("%s kind = %v, want producer", typ, s.SpanKind())
		}
		if s.Parent().SpanID() != parent.SpanContext().SpanID() {
			t.Errorf("%s span is not a child of the caller's span", typ)
		}
		wantAttrs(t, s, map[attribute.Key]string{"vda5050.serial_number": "R1", "vda5050.message.type": typ,
			"messaging.destination.name": "vda5050/v3/EMU/R1/" + typ})
	}
	if s := spanNamed(t, rec, "publish vda5050 factsheet"); s.Status().Code != codes.Error {
		t.Errorf("failed publish status = %v, want Error", s.Status())
	}
}
