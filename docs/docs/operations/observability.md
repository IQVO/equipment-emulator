---
id: observability
title: Observability
sidebar_position: 3
description: "Logs, traces and metrics: the fleet OpenTelemetry standard, applied to the emulator."
---

# Observability

The emulator follows the fleet observability standard
([ADR 0005](/docs/adr/0005-opentelemetry-traces-and-metrics)): JSON logs on stdout, and
traces and metrics exported over OTLP/gRPC to the OpenTelemetry Collector.

## Turning it on

Telemetry is **off** unless `OTEL_EXPORTER_OTLP_ENDPOINT` is set (host:port or a URL). The
Helm chart sets it while `otel.enabled` (default on, the cluster Collector); a `go run`
or an e2e container exports nothing. Export never blocks: a Collector that is down costs
telemetry, never readiness.

| Variable | Default | Meaning |
| --- | --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty (off) | Collector OTLP/gRPC endpoint |
| `OTEL_SERVICE_NAME` | `equipment-emulator` | `service.name` |
| `SERVICE_VERSION` | `dev` | `service.version` (the chart passes the image tag) |
| `ENVIRONMENT` | `local` | `deployment.environment.name` |

## Logs

Every line is JSON. A line written while a span is active (an admin request, an MQTT
message being handled) carries `trace_id` and `span_id`. Each admin request writes one
`http request` line (`method`, `path`, `status`, `duration_ms`, `bytes`, `request_id`).

| Message | Level | Meaning |
| --- | --- | --- |
| `emulator running` | INFO | boot finished; carries `robots`, `asrs`, `simSpeed` |
| `telemetry configured` / `telemetry disabled: ...` | INFO | whether an exporter was installed |
| `emulator stopped` | ERROR | boot or the HTTP server failed; the process exits 1 |
| `order rejected` | INFO | the robot refused an order (`err` names the VDA 5050 error type) |
| `unsupported order` | WARN | an order with an edge action or a node without `nodePosition` was **dropped**; fleet control gets no feedback ([ADR 0002](/docs/adr/0002-vda5050-v3-robot-side-subset)) |
| `malformed order` / `malformed instantActions` / `malformed telegram` | WARN | the message did not decode |
| `telegram never acknowledged` | WARN | an up telegram ran out of retransmissions |
| `message on an unexpected topic` | WARN | a message arrived on a robot topic the emulator does not handle |
| `resubscribe failed` / `re-announce failed` | WARN | a reconnect could not restore a robot's subscriptions or announce it |
| `step` | WARN | a simulation step failed to publish |

## Traces

- **Admin API**: one server span per request, named after the chi route pattern
  (`/asrs/{asrsId}`, `/faults`).
- **VDA 5050**: `receive vda5050 order|instantActions` (consumer) and
  `publish vda5050 state|connection|factsheet` (producer), with `vda5050.serial_number` and
  `vda5050.message.type`; an order's span adds `vda5050.order.id`, `outcome` and `reason`.
- **Telegrams**: `receive telegram <TYPE>` and `publish telegram <TYPE>`, with `asrs.id`,
  `telegram.id`, `telegram.type` and, on a publish, `telegram.retransmit`.

A publish is a child of whatever caused it: the state after an order, the ACK of a
telegram, the connection message of a `ROBOT_DISCONNECT` fault injected over REST.
MQTT 3.1.1 carries no headers and neither protocol has a trace field, so a controller's
trace does **not** continue into the emulator: each receive span starts a trace.

## Metrics

| Instrument | Type | Attributes |
| --- | --- | --- |
| `emulator.orders.received` | counter `{order}` | `outcome` (`accepted`, `rejected`), `reason` on a rejection |
| `emulator.telegrams` | counter `{telegram}` | `telegram.direction` (`down`, `up`), `telegram.type` |
| `emulator.telegrams.retransmits` | counter `{telegram}` | `telegram.type` |
| `emulator.telegrams.expired` | counter `{telegram}` | `telegram.type` |
| `emulator.robots` | gauge `{robot}` | `robot.state`: `disconnected`, `error`, `charging`, `driving`, `idle` |
| `emulator.simclock.ratio` | gauge `1` | none: simulated seconds per wall second since start |
| `http.server.request.duration`, `http.server.active_requests` | otelchi | `http.route`, method (no status code: see INCIDENTS.md, 2026-10-12) |
| `go.*` | Go runtime instrumentation | none |

In Prometheus the dots become underscores and counters get `_total`
(`emulator_orders_received_total`). Questions they answer:

- *Is the controller sending orders the robot refuses?*
  `sum by (reason) (rate(emulator_orders_received_total{outcome="rejected"}[5m]))`
- *Is the WCS acknowledging telegrams?* A non-zero
  `rate(emulator_telegrams_retransmits_total[5m])`, and any `emulator_telegrams_expired_total`.
- *Is the simulation keeping up with `SIM_SPEED`?* `emulator_simclock_ratio` below the
  configured speed means ticks fall behind.

The wire itself is still the most detailed trace: subscribe to `vda5050/v3/#` and
`asrs/v1/#` on the broker (see the [Quickstart](/docs/overview/quickstart)).
