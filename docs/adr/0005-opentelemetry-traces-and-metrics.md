# ADR 0005: OpenTelemetry traces and metrics, per the fleet observability standard

- Status: Accepted (2026-11-20). Applies the fleet observability standard (outbound-shipping ADR 0008,
  inventory-storage ADR 0016) to the emulator; the telemetry package is the customer-returns / outbound-shipping
  shape. Builds on [ADR 0001](/docs/adr/0001-emulator-role-and-boundaries) and
  [ADR 0004](/docs/adr/0004-simulated-time-fault-injection-and-admin-api).

## Context

Until now `cmd/emulator` wrote JSON logs only: no tracer or meter provider, no exporter, no `OTEL_*` variable, and a
plain chi router. A WCS test that goes wrong (an order the robot never accepted, a telegram retransmitted until it
expired) could only be read back from the broker or the log. The fleet pipeline (OTLP/gRPC to the Collector, then
Jaeger and Prometheus) saw nothing of the emulator.

## Decision

**Tier 1, the same as every fleet service.**

- `internal/adapters/outbound/telemetry` owns `Setup` (OTLP/gRPC trace and metric exporters, the W3C trace-context
  and baggage propagator, Go runtime metrics) and `WithTraceContext`, the slog wrapper that stamps `trace_id` and
  `span_id` on records written with a context that carries a span. `cmd/emulator` builds its logger with it and
  makes it `slog.Default`, so the SDK's own diagnostics stay JSON too.
- **An empty `OTEL_EXPORTER_OTLP_ENDPOINT` means telemetry is off** (outbound-shipping's rule, not
  customer-returns' `localhost:4317` default): `Setup` installs nothing and the flush is a no-op, so a laptop run or
  an e2e container (asrs-control's integration test) never dials a Collector. With an endpoint, export is
  non-blocking: a Collector that is down costs telemetry, never readiness or startup.
- `cmd/emulator/telemetry.go` reads `OTEL_SERVICE_NAME` (default `equipment-emulator`), `SERVICE_VERSION` and the
  endpoint; `ENVIRONMENT` (`deployment.environment.name`) is read by `Setup`.
- Admin router middleware, in the fleet order: `RequestID`, `otelchi.Middleware` (server span named after the chi
  route pattern), `otelchimetric` request duration and active requests, `requestlog.RequestLogger`, chi's
  `Recoverer`. `RequestLogger` is its own package (`internal/adapters/inbound/requestlog`), a copy of the fleet's.

**Traces over MQTT.** `internal/adapters/mqtt` opens one span per message:

| Span | Kind | Where | Attributes |
| --- | --- | --- | --- |
| `receive vda5050 order` / `receive vda5050 instantActions` | consumer | `RobotGateway.dispatch` | `vda5050.serial_number`, `vda5050.message.type`; an order adds `vda5050.order.id`, `outcome`, `reason` |
| `publish vda5050 state` / `connection` / `factsheet` | producer | `RobotGateway.send` | `vda5050.serial_number`, `vda5050.message.type` |
| `receive telegram <TYPE>` | consumer | `AsrsGateway.handle` | `asrs.id`, `telegram.id`, `telegram.type` |
| `publish telegram <TYPE>` | producer | `AsrsGateway.send` | `asrs.id`, `telegram.id`, `telegram.type`, `telegram.retransmit` |

Every span also carries `messaging.system=mqtt` and `messaging.destination.name` (the topic). A publish is a child of
the span in its context: the state published after an order is a child of that order's receive span, the ACK of a
telegram a child of its receive span, and a `ROBOT_DISCONNECT` fault's connection message a child of the admin
request's server span. Tick-driven publishes (the 30 s state, AS/RS events) start their own trace.

**No trace context crosses MQTT.** The emulator speaks MQTT 3.1.1 (paho v1), which has no message headers, and
neither VDA 5050 v3 nor telegram protocol v1 has a field for one. Adding a field would change the wire the
controllers are tested against (ADR 0001), so a receive span is the root of its trace and a controller's trace does
not continue into the emulator. Joining them needs MQTT 5 user properties on both sides: a later decision.

**Metrics** (`internal/adapters/outbound/telemetry/emulator_metrics.go`). The emulator has no domain outcome of its
own, but it is the only place that sees what the controllers sent, so the counters describe that traffic:

| Instrument | Type, unit | Attributes | Recorded |
| --- | --- | --- | --- |
| `emulator.orders.received` | counter, `{order}` | `outcome` (`accepted`, `rejected`), `reason` on a rejection: the VDA 5050 error type, `ROBOT_DISCONNECTED`, `UNKNOWN_ROBOT`, `NOT_READY`, `malformed` or `unsupported` | every order message, in `RobotGateway.onOrder` |
| `emulator.telegrams` | counter, `{telegram}` | `telegram.direction` (`down`, `up`), `telegram.type` | every down telegram received, every up telegram's first send |
| `emulator.telegrams.retransmits` | counter, `{telegram}` | `telegram.type` | every resend of an unacknowledged up telegram |
| `emulator.telegrams.expired` | counter, `{telegram}` | `telegram.type` | an up telegram given up after the last retransmission |
| `emulator.robots` | gauge, `{robot}` | `robot.state`: `disconnected`, `error` (an URGENT/CRITICAL/FATAL error), `charging`, `driving`, `idle` (in that precedence) | observed at each collection from the engine; every state is reported, zeros included |
| `emulator.simclock.ratio` | gauge, `1` | none | simulated seconds elapsed per wall second since start: below `SIM_SPEED` when ticks fall behind |

A telegram type outside protocol v1 is labelled `UNKNOWN`, so a sender's typo cannot grow the series. The counters
are recorded through the `mqtt.Metrics` port (nil-safe; the telemetry adapter implements it structurally and
`cmd/emulator` wires it), never from `internal/domain` or `internal/application`. The engine gained one read-only
query, `IsDisconnected`, for the gauge. A metric name or attribute is a contract with every dashboard: renaming one
needs a new ADR.

**Chart.** `otel.{enabled,endpoint,serviceName}`, `serviceVersion` (empty: `image.tag`, then `appVersion`) and
`environment`; the Deployment renders `ENVIRONMENT` and `SERVICE_VERSION` always, `OTEL_EXPORTER_OTLP_ENDPOINT` and
`OTEL_SERVICE_NAME` only while `otel.enabled`, and `extraEnv` refuses those four names. `tests/render_test.sh` now
scans `internal/config`, `cmd/emulator` and the telemetry adapter for every env name the binary reads (floor 8) and
asserts the Deployment renders each one.

## Consequences

- A WCS test run in the cluster shows the emulator in Jaeger and Prometheus: an order rejection or a retransmit storm
  is a series, not a grep.
- Off by default outside the chart: `go run` and e2e containers export nothing unless the endpoint is set.
- Dashboards and alert rules belong to warehouse-infra and are not part of this change.
- New direct dependencies: the OTel SDK and OTLP/gRPC exporters, `otelchi`, `go-logr/logr`; `govulncheck ./...`
  is a required check.
