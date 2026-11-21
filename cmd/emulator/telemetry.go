package main

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/IQVO/equipment-emulator/internal/adapters/mqtt"
	"github.com/IQVO/equipment-emulator/internal/adapters/outbound/telemetry"
	"github.com/IQVO/equipment-emulator/internal/application/engine"
	"github.com/IQVO/equipment-emulator/internal/domain/simclock"
)

// defaultServiceName is the OTEL_SERVICE_NAME default: the service.name of
// every span and metric of this binary.
const defaultServiceName = "equipment-emulator"

// telemetryFlushTimeout bounds the final export attempt, so an unreachable
// Collector cannot stretch a shutdown past what an orchestrator waits for.
const telemetryFlushTimeout = 5 * time.Second

// telemetryEnv is the observability part of the environment (ADR 0005).
//
//	OTEL_SERVICE_NAME            service.name (default equipment-emulator)
//	OTEL_EXPORTER_OTLP_ENDPOINT  Collector OTLP/gRPC endpoint, host:port or URL; empty = telemetry off
//	SERVICE_VERSION              service.version (default telemetry.DefaultServiceVersion)
//	ENVIRONMENT                  deployment.environment.name, read by telemetry.Setup
type telemetryEnv struct {
	serviceName    string
	serviceVersion string
	otlpEndpoint   string
}

func loadTelemetryEnv(env func(string) string) telemetryEnv {
	name := env("OTEL_SERVICE_NAME")
	if name == "" {
		name = defaultServiceName
	}
	return telemetryEnv{serviceName: name, serviceVersion: env("SERVICE_VERSION"), otlpEndpoint: env("OTEL_EXPORTER_OTLP_ENDPOINT")}
}

// newLogger is the process logger: JSON on w, stamped with trace_id/span_id
// whenever a record is written with a context that carries a span.
func newLogger(w io.Writer) *slog.Logger {
	return slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(w, nil)))
}

// startTelemetry brings OpenTelemetry up and returns the flush func to defer.
// Export is non-blocking: an unreachable Collector costs telemetry, never
// availability. With no endpoint nothing is installed. A failed flush is
// logged, never returned: a clean shutdown must not look like a crash. It must
// run before the router is built: otelchimetric binds the global MeterProvider then.
func startTelemetry(logger *slog.Logger, cfg telemetryEnv) (func(), error) {
	shutdown, err := telemetry.Setup(context.Background(), cfg.serviceName, cfg.serviceVersion, cfg.otlpEndpoint)
	if err != nil {
		return nil, err
	}
	if cfg.otlpEndpoint == "" {
		logger.Info("telemetry disabled: OTEL_EXPORTER_OTLP_ENDPOINT is not set", "service_name", cfg.serviceName)
	} else {
		logger.Info("telemetry configured", "service_name", cfg.serviceName, "otlp_endpoint", cfg.otlpEndpoint)
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), telemetryFlushTimeout)
		defer cancel()
		if err := shutdown(ctx); err != nil {
			logger.Warn("telemetry flush failed on shutdown", "error", err)
		}
	}, nil
}

// instrument installs the MQTT counters on the gateways (before they start)
// and registers the robots-by-state and simulated-time-ratio gauges. A
// registration error (an invalid instrument name: a programming error) is
// logged, never fatal: the emulator then runs without that metric.
func instrument(logger *slog.Logger, eng *engine.Engine, clock *simclock.Sim, rg *mqtt.RobotGateway, router mqtt.AsrsRouter) {
	m, err := telemetry.NewEmulatorMetrics()
	if err != nil {
		logger.Warn("emulator counters unavailable", "error", err)
	} else {
		rg.SetMetrics(m)
		router.SetMetrics(m)
	}
	if err := telemetry.RegisterGauges(eng, telemetry.SimRatio(clock.Now, time.Now)); err != nil {
		logger.Warn("emulator gauges unavailable", "error", err)
	}
}
