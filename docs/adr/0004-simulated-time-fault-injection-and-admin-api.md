# ADR 0004: Simulated time, fault injection and an unauthenticated admin REST API

- Status: Accepted (2026-11-20). Records what `develop` implements (PR #2); the REST contract is `apis/openapi.yaml`.
- Builds on [ADR 0001](/docs/adr/0001-emulator-role-and-boundaries). Fleet rule: `.claude/rules/fleet/no-auth-and-mcp.md`
  (no auth layer, fleet decision 2026-09-11).

## Context

Tests of the WCS controllers need three things a real device does not offer: a shift that runs faster than real
time, faults on demand (a blocked robot, an empty battery, a held aisle unit, an unreadable label) and a way to
put initial stock into the rack. None of these may become a second command channel: robots and PLCs are commanded
only over MQTT, as on the shop floor.

## Decision

1. **Simulated time.** `internal/domain/simclock` holds a monotonic simulated clock that only the engine advances.
   `cmd/emulator/main.go` ticks every `TICK_INTERVAL` (default `100ms`) of wall time and advances the clock by
   `simclock.Scale(TICK_INTERVAL, SIM_SPEED)`; `SIM_SPEED` (default `1`) is simulated seconds per wall-clock second.
   `internal/config/config.go` refuses a non-positive `SIM_SPEED` or `TICK_INTERVAL` at boot. Simulated time
   starts at the process start. Every published message carries it, except the robots' static `CONNECTION_BROKEN`
   last will (stored by the broker at connect). The AS/RS retransmit timer runs on real time
   ([ADR 0003](/docs/adr/0003-asrs-telegram-protocol-v1)): it is a property of the wire, not of the simulation.
2. **Fault injection.** `POST /faults` switches a fault on or off with `active`, so a repeated request converges to
   the same state (`internal/application/engine/engine.go`, `InjectFault`):
   `ROBOT_BLOCKED` (the robot stops, `CRITICAL` error), `ROBOT_BATTERY` (sets the state of charge),
   `ROBOT_DISCONNECT` (the robot publishes nothing and drops every received message until cleared, then
   re-announces), `ASRS_UNIT` (holds an aisle unit, `HELD`), `ASRS_NO_READ` (an output port reports arrivals
   without `containerId`).
3. **Admin REST API** (`internal/adapters/inbound/http/router.go`, chi): `GET /healthz`, `GET /readyz` (503 until
   every MQTT client is connected), `GET /robots`, `GET /asrs/{asrsId}`, `POST /faults`, `POST /asrs/{asrsId}/seeds`
   (put a container into a rack position), `POST /asrs/{asrsId}/ports/{portId}/container` (put a container on an
   input port, as a robot drop would). It inspects, seeds and injects faults; it never orders a robot or submits
   a PLC job. Every error is `application/problem+json`.
4. **No authentication.** The admin API has no auth layer, like every REST surface of the fleet since the
   2026-09-11 revert; `TestNoAuthMiddlewareReintroduced` guards it. Re-adopting auth is a fleet decision with its
   own ADR.

## Consequences

- At `SIM_SPEED` 10 a shift runs in a tenth of its length, and the robots' 30 s state heartbeat comes every 3 s of
  wall time; the AS/RS retransmit still waits 2 s of wall time. A test that compares timestamps must use the
  message timestamps, not the wall clock.
- Anything that can reach the admin port can inject faults and seed stock. The emulator therefore runs only in
  development, CI and the test namespace of the cluster, never next to real equipment.
- The admin API is a REST contract like any other: Spectral-linted, gated by `spec-breaking`, and published on the
  docs site from `apis/openapi.yaml` (the `docs-api-drift` job keeps the generated pages in sync).
