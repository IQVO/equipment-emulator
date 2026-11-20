package http

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/IQVO/equipment-emulator/internal/application/engine"
)

// spanAdmin remembers the span context each fault injection ran under.
type spanAdmin struct {
	fakeAdmin
	seen []trace.SpanContext
}

func (s *spanAdmin) InjectFault(ctx context.Context, f engine.Fault) error {
	s.seen = append(s.seen, trace.SpanContextFromContext(ctx))
	return s.fakeAdmin.InjectFault(ctx, f)
}

func TestRouterTracesEachRequestByRoutePatternAndLogsIt(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	var logs bytes.Buffer
	a := &spanAdmin{}
	h := NewRouter(a, func() bool { return true }, slog.New(slog.NewJSONHandler(&logs, nil)), "equipment-emulator")

	if r := do(h, "GET", "/asrs/ASRS1", ""); r.Code != 200 {
		t.Fatalf("asrs %d", r.Code)
	}
	if r := do(h, "POST", "/faults", `{"kind":"ROBOT_BLOCKED","target":"R1","active":true}`); r.Code != 204 {
		t.Fatalf("fault %d", r.Code)
	}

	names := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range rec.Ended() {
		names[s.Name()] = s
	}
	get, ok := names["/asrs/{asrsId}"]
	if !ok {
		t.Fatalf("no server span named after the route pattern; spans: %v", names)
	}
	if get.SpanKind() != trace.SpanKindServer {
		t.Errorf("kind = %v, want server", get.SpanKind())
	}
	post, ok := names["/faults"]
	if !ok {
		t.Fatalf("no span for POST /faults; spans: %v", names)
	}
	if len(a.seen) != 1 || a.seen[0].SpanID() != post.SpanContext().SpanID() {
		t.Error("the fault injection did not run under the request's server span")
	}

	var first map[string]any
	if err := json.Unmarshal(bytes.SplitN(logs.Bytes(), []byte("\n"), 2)[0], &first); err != nil {
		t.Fatalf("access log is not JSON: %v (%q)", err, logs.String())
	}
	if first["msg"] != "http request" || first["path"] != "/asrs/ASRS1" || first["status"] != float64(200) || first["request_id"] == "" {
		t.Errorf("access log line = %v", first)
	}
}
