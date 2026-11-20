package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// The telemetry environment: OTel spec names plus the fleet's SERVICE_VERSION.
// An unset endpoint means telemetry is off (ADR 0005).
func TestLoadTelemetryEnvDefaults(t *testing.T) {
	cfg := loadTelemetryEnv(envOf(nil))
	if cfg.serviceName != "equipment-emulator" {
		t.Errorf("serviceName = %q, want equipment-emulator", cfg.serviceName)
	}
	if cfg.serviceVersion != "" {
		t.Errorf("serviceVersion = %q, want empty (Setup falls back to dev)", cfg.serviceVersion)
	}
	if cfg.otlpEndpoint != "" {
		t.Errorf("otlpEndpoint = %q, want empty: telemetry is off unless configured", cfg.otlpEndpoint)
	}
}

func TestLoadTelemetryEnvFromEnvironment(t *testing.T) {
	cfg := loadTelemetryEnv(envOf(map[string]string{
		"OTEL_SERVICE_NAME":           "equipment-emulator-e2e",
		"SERVICE_VERSION":             "v1.2.3",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "otel-collector:4317",
	}))
	if cfg.serviceName != "equipment-emulator-e2e" || cfg.serviceVersion != "v1.2.3" || cfg.otlpEndpoint != "otel-collector:4317" {
		t.Errorf("telemetry config = %+v", cfg)
	}
}

// Every log line written with a context that carries a span is stamped with
// trace_id/span_id: the logger the composition root builds must correlate.
func TestNewLoggerStampsTraceAndSpanIDs(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf)

	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))
	logger.InfoContext(ctx, "order rejected")

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line is not valid JSON: %v", err)
	}
	if line["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" || line["span_id"] != "00f067aa0ba902b7" {
		t.Errorf("trace_id/span_id = %v/%v, want the active span's ids", line["trace_id"], line["span_id"])
	}
}

func TestStartTelemetryWithoutAnEndpointIsANoOp(t *testing.T) {
	var buf bytes.Buffer
	flush, err := startTelemetry(newLogger(&buf), telemetryEnv{serviceName: "equipment-emulator"})
	if err != nil {
		t.Fatalf("startTelemetry: %v", err)
	}
	flush() // must not block or panic
	if !bytes.Contains(buf.Bytes(), []byte("telemetry disabled")) {
		t.Errorf("log = %q, want the telemetry-disabled line", buf.String())
	}
}
