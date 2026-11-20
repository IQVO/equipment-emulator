# SPEC.md — equipment-emulator

The agent entry point is `CLAUDE.md` (`AGENTS.md` is a symlink to it). This file is the longer
description of what the emulator is and does, as built on `develop`. The wire contracts are
`apis/asyncapi.yaml` (MQTT) and `apis/openapi.yaml` (admin REST); when this file and a contract
disagree, the contract wins and this file is fixed.

## Role

The equipment emulator stands in for ISA-95 levels 1 and 0 of the WCS tier: the robots and the
AS/RS PLC that this study project does not have. The WCS strategic design (warehouse-docs,
`docs/wcs/subdomains.md` and `docs/wcs/implementation-handoff.md`, "Emulator scope") classes
*equipment emulation* as **not a domain**: test infrastructure that holds no business rule. It is
step 1 of the WCS build order, so `asrs-control` and `amr-fleet-control` can be built and tested
against it.

- It is **not a bounded context**: no Kafka, no Postgres, no CloudEvents, no outbox, no MCP or A2A
  surface.
- It calls **no sibling service**. It only answers MQTT traffic and serves its own admin REST API.
- It owns **no business data**. All state is in memory and lost on restart; the world is loaded
  from a YAML file at boot.

## Process

One binary, `cmd/emulator`. It reads the world file, builds the simulated devices, connects one
MQTT client per robot plus one for the AS/RS PLCs, starts the admin HTTP server and runs the
simulation loop.

| Variable | Default | Meaning |
| --- | --- | --- |
| `WORLD_FILE` | required | YAML world: robots and AS/RS systems (`config/world.example.yaml`) |
| `MQTT_URL` | `tcp://localhost:1883` | MQTT broker |
| `HTTP_ADDR` | `:8080` | Admin API listen address |
| `SIM_SPEED` | `1` | Simulated seconds per wall-clock second |
| `TICK_INTERVAL` | `100ms` | Wall-clock interval between simulation ticks |

**Simulated time.** `internal/domain/simclock` holds a monotonic simulated clock that only the
engine advances: each tick advances it by `TICK_INTERVAL × SIM_SPEED` (`internal/config` refuses a
non-positive `SIM_SPEED` or `TICK_INTERVAL` at boot). It starts at process start. Every published message is stamped with simulated
time, except the robots' static `CONNECTION_BROKEN` last will; the AS/RS 2 s retransmit timer runs
on real time. At `SIM_SPEED` 10 a full shift runs in a tenth of its length.

**World file.** `robots[]` (manufacturer, serial number, start pose and map, speed, battery drain
per metre, charge rate, action duration) and `asrs[]` (id, aisles, levels, slots, double-deep,
timing, ports). Each port names the VDA 5050 `nodeId` where robots drop at or pick from it; that
mapping is how a robot's `pick`/`drop` reaches the AS/RS. The Helm chart renders the same shape
from `values.yaml` `world:` into a ConfigMap.

## Simulated device 1: AMR robots (VDA 5050 v3.0.0)

Each robot implements the robot side of VDA 5050 v3.0.0, JSON over MQTT 3.1.1, QoS 1, topics
`vda5050/v3/<manufacturer>/<serialNumber>/<topic>`. Full rules: `docs/vda5050-subset.md`.

| Topic | Direction | Behaviour |
| --- | --- | --- |
| `order` | fleet control → robot | orders and order updates (stitched at the last released node) |
| `instantActions` | fleet control → robot | `cancelOrder`, `startCharging`, `stopCharging`, `stateRequest`, `factsheetRequest` |
| `state` | robot → fleet control | on every change, at once after every order/instant-action message, and at least every 30 s of simulated time |
| `connection` | robot → fleet control | retained `ONLINE` / `OFFLINE`; `CONNECTION_BROKEN` as the retained last will |
| `factsheet` | robot → fleet control | retained; subset (`typeSpecification`, `physicalParameters`) |

The robot drives the released base node by node at its configured speed (capped by an edge's
`maxSpeed`), runs node actions `pick` (`loadId`), `drop`, `startCharging`, `stopCharging` one at a
time for the configured action duration, drains its battery per metre and charges per second.
Rejections are reported as `WARNING` errors in the next `state` (there is no reply topic). The
emulator adds three `errorType` values: `LOAD_NOT_AVAILABLE`, `ROBOT_BLOCKED`, `BATTERY_EMPTY`.

### Partial: what the VDA 5050 subset leaves out

- **Edge actions are not supported.** An order with any edge action, or with a node without
  `nodePosition`, is dropped by the adapter and logged; it is **not** reported in `state`, so fleet
  control gets no feedback for it.
- No trajectories, corridors or zones (`zoneSet`), no `visualization` topic, no `responses`, no
  map download.
- `blockingType` is accepted but ignored (actions run sequentially); `paused` is always `false`;
  `HIBERNATING` is never published; `operatingMode` is always `AUTOMATIC`; `safetyState` never
  reports an emergency stop or field violation.
- The factsheet is a subset (`typeSpecification`, `physicalParameters`).
- `errors` and `instantActionStates` keep only the 50 most recent entries.

## Simulated device 2: AS/RS PLC (telegram protocol v1)

Each AS/RS implements the PLC side of the emulator's own telegram protocol v1 (no public standard
exists; the shape follows SAP EWM telegrams). JSON over MQTT 3.1.1, QoS 1, not retained. Full
rules: `docs/telegram-protocol.md`.

| Topic | Direction | Telegrams |
| --- | --- | --- |
| `asrs/v1/<asrsId>/down` | WCS → PLC | `STORE`, `RETRIEVE`, `MOVE`, `CANCEL`, `STATE_REQUEST`, and ACKs of up telegrams |
| `asrs/v1/<asrsId>/up` | PLC → WCS | `ACK`, `JOB_DONE`, `JOB_FAILED`, `UNIT_STATE`, `PORT_ARRIVAL` |

- **Handshake.** Every non-ACK telegram is acknowledged; `accepted: false` carries one bare reason
  code. An unacknowledged up telegram is retransmitted after 2 s (real time), at most 3 times. A
  repeated `telegramId` is not processed again; the first ACK is resent. `NOT_READY` (still
  starting) is transient and not remembered.
- **Model.** Rack positions `aisle/level/slot/depth` (depth 2 only in double-deep racks; a back
  position is blocked while its front is occupied). One combined shuttle-and-lift unit per aisle
  runs jobs one at a time in FIFO order; job time = base + per-level × level + per-slot × slot.
  Unit states use PackML names `IDLE`, `EXECUTE`, `HELD`.
- **Outcomes.** An accepted job ends in exactly one `JOB_DONE` or `JOB_FAILED`; a RETRIEVE's
  `JOB_DONE` is followed by a `PORT_ARRIVAL` at its output port (no `containerId` = no-read). Input
  ports emit no `PORT_ARRIVAL`. A `jobId` is never reusable within the process lifetime. A queued
  job can be cancelled; a running one cannot.

## Admin REST API (control operations)

Specified in `apis/openapi.yaml`; unauthenticated (fleet rule); every error is
`application/problem+json` with a stable `code`. Every POST takes an optional `Idempotency-Key`
(in-memory store, 24 h, lost on restart; ADR 0006). Route-to-engine mapping:
`.claude/rules/rest-api.md`.

| Method and path | Operation | Purpose |
| --- | --- | --- |
| `GET /healthz` | `healthz` | Liveness |
| `GET /readyz` | `readyz` | 503 until the MQTT broker is connected |
| `GET /robots` | `listRobots` | One keyset page (`limit`, `cursor`; `{items, nextCursor}`) of every robot's order, last node, pose, battery, load and active error types |
| `GET /asrs/{asrsId}` | `getAsrs` | Rack slots, port contents and unit states of one AS/RS |
| `POST /faults` | `injectFault` | Set or clear a fault: `ROBOT_BLOCKED`, `ROBOT_BATTERY` (set state of charge), `ROBOT_DISCONNECT`, `ASRS_UNIT` (hold an aisle unit), `ASRS_NO_READ` (output port) |
| `POST /asrs/{asrsId}/seeds` | `seedContainer` | Put a container into a rack position (initial stock) |
| `POST /asrs/{asrsId}/ports/{portId}/container` | `placeContainerAtPort` | Put a container on an input port, as if a robot dropped it |

A fault-disconnected robot publishes nothing and drops every `order` and `instantActions` message
until the fault is cleared, then re-announces `ONLINE` and its state. The admin API never commands
devices: orders and jobs arrive only over MQTT.

## Quality evidence

Unit tests on `internal/domain/{robot,asrs,simclock}` and the engine (90% coverage gate on domain
and application), godog features in `features/` (robot block/unblock, AS/RS no-read and unit
fault), `-tags=integration` gateway tests against Mosquitto 2.0.20 via testcontainers, mutation
testing on `internal/domain/robot` (thresholds in `.gremlins.yaml`), Spectral lint of both
contracts, arch-go fitness tests (`internal/architecture/`). Commands: see `CLAUDE.md`.

## Planned, not built

- Seeding the robot map from `facility-layout`'s travel graph (the design's "seeded from the
  fleet"); today the world file is written by hand and `amr-fleet-control`'s map must use the same
  node ids.
- The first `develop` → `main` release (`docs/before-first-release.md`); there is no `main`
  branch yet.
- Deployment through `warehouse-infra` next to the WCS Mosquitto broker.

## Non-goals

- Business rules of any kind: slotting, tote selection, routing, traffic control, capacity
  planning. Those belong to `asrs-control`, `amr-fleet-control` and `material-flow-control`.
- Durable state, Kafka events, CloudEvents, an outbox, or any call to a sibling context.
- Physical fidelity: no kinematics beyond straight-line moves at constant speed, no collision
  model between robots, no separate shuttle and lift timing.
- Full VDA 5050 conformance (see the subset above) or a vendor-certified PLC protocol.
- Authentication on the admin API.
