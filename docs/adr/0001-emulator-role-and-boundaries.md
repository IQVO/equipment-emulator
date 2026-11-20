# ADR 0001: The emulator is test infrastructure at ISA-95 L1/L0, not a bounded context

- Status: Accepted (2026-11-20). Records the role the code on `develop` already has (PR #2, `feature/emulator-core`).
- Design source: warehouse-docs `docs/wcs/implementation-handoff.md` ("Emulator scope", build order step 1) and
  `docs/wcs/subdomains.md`, which class *equipment emulation* as not a domain.

## Context

The WCS tier of the fleet (`material-flow-control` over the subsystem controllers `amr-fleet-control` and
`asrs-control`) talks to hardware the study project does not have: AMR robots and an AS/RS PLC, ISA-95 levels 1
and 0. The subsystem controllers need a counterpart to be built and tested against, on a laptop, in CI and on the
cluster. The WCS strategic design makes that counterpart step 1 of the build order and states that it holds no
business rule.

Every other repository of the fleet is a bounded context and carries the bounded-context kit: Kafka with
CloudEvents and a transactional outbox, Postgres, an MCP server, an A2A agent, a Kong route, a DDD artifact pack.
Applying that kit here by habit would turn a simulator into a fake context with its own model of stock and jobs.

## Decision

`equipment-emulator` is **test infrastructure**, a simulated device at ISA-95 L1/L0. It is not a bounded context
and owns no business data.

1. **What it simulates.** AMR robots that speak the robot side of VDA 5050 v3 ([ADR 0002](/docs/adr/0002-vda5050-v3-robot-side-subset))
   and AS/RS PLCs that speak the emulator's own telegram protocol v1 ([ADR 0003](/docs/adr/0003-asrs-telegram-protocol-v1)),
   both JSON over MQTT 3.1.1 at QoS 1 (`internal/adapters/mqtt`, `internal/adapters/vda5050`,
   `internal/adapters/telegram`).
2. **No business rules.** Slotting, tote selection, routing, traffic control, job scheduling across aisles and
   capacity planning belong to `asrs-control`, `amr-fleet-control` and `material-flow-control`. The emulator only
   checks what a device would check (an order's node chain, a free rack position, a container at a port) and
   reports the result on the wire.
3. **No Kafka, no database, no CloudEvents, no outbox.** All state is in memory (`internal/domain/robot`,
   `internal/domain/asrs`, `internal/application/engine`) and lost on restart. The world is loaded from the YAML
   file named by `WORLD_FILE` at boot (`internal/config/config.go`, `internal/application/engine/world.go`).
   `TestRuntimeBinaryHasNoInMemoryAdapters` and `TestNoGoFilesAtRepoRoot` (`internal/architecture/`) still apply:
   the in-memory state is the real runtime, not a test double standing in for a store.
4. **No calls to sibling contexts.** It answers MQTT traffic and serves its own admin REST API
   ([ADR 0004](/docs/adr/0004-simulated-time-fault-injection-and-admin-api)); it never calls a fleet service.
5. **One binary**, `cmd/emulator`, which wires the gateways, the engine and the admin HTTP server
   (`cmd/emulator/main.go`). The hexagonal layering of the fleet applies (`internal/architecture/architecture_test.go`).
6. **Fleet surfaces that do not apply** and are therefore absent by decision, not by omission: the MCP server, the
   A2A agent, the Kafka consumer and publisher wiring, Postgres and migrations, the Kong/Gateway API route, and the
   per-context DDD artifact pack. The fleet-managed sensors that check those surfaces (Kafka, outbox and MCP
   fitness tests copied from warehouse-harness-template) find nothing to check here and stay as copies.

## Consequences

- The WCS controllers are developed against a device they cannot tell apart from the real protocol peer, within
  the subset each ADR lists. Where the emulator is simpler than hardware (no kinematics beyond straight lines, one
  combined shuttle-and-lift per aisle) the controllers' tests inherit that simplification.
- Restarting the emulator resets the world. Tests seed what they need through the admin API.
- A request to add a business rule, durable state or a Kafka topic here is a request to create a new context
  and goes to the WCS strategic design first, not into this repository.
- The fleet standard applies where it concerns test infrastructure: the quality harness, the CI pipeline, the Helm
  chart and its tests, the docs site, the README skeleton. Observability (OpenTelemetry) and the first release
  are later waves.
