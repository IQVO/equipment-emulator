# Domain model: simulated devices, their invariants, the engine

The emulator is test infrastructure, not a bounded context: its "domain" is the simulation model
of the equipment, with no business rule. The domain packages know nothing about MQTT, JSON or
HTTP (`TestDomainHasNoJSONTags` and `make arch-test` enforce it). Wire names belong to the
adapters (`internal/adapters/vda5050`, `internal/adapters/telegram`).

## Ubiquitous language (use these exact names)

- **Robot** — one simulated AMR as VDA 5050 v3 sees the robot side (`internal/domain/robot`).
- **Order** / **order update** — a VDA 5050 order: `Nodes` joined by `Edges`; an update keeps the
  `orderId` and raises `UpdateID`.
- **Base** / **horizon** — the released nodes the robot may drive / the unreleased tail it may not.
- **Node action** — `pick` (param `loadId`), `drop`, `startCharging`, `stopCharging`.
  **Instant action** — `cancelOrder`, `startCharging`, `stopCharging`, `stateRequest`,
  `factsheetRequest`.
- **Peripherals** — the world around a robot: a `pick`/`drop` at a node mapped to an AS/RS port
  must be agreed by that port.
- **System** — one simulated AS/RS as its PLC sees it (`internal/domain/asrs`).
- **Position** — `Aisle/Level/Slot/Depth`, printed `A1-L2-S10-D1`; depth 2 exists only in a
  double-deep rack.
- **Port** — an `INPUT` or `OUTPUT` port on one aisle; holds at most one container.
- **Job** — `STORE` (input port → position), `RETRIEVE` (position → output port), `MOVE`
  (position → position, same aisle).
- **Unit** — the combined shuttle-and-lift of one aisle, id `A<aisle>`, state `IDLE` | `EXECUTE` |
  `HELD` (PackML names).
- **No-read** — a `PORT_ARRIVAL` without a container id (barcode fault on an output port).
- **Fault** — an admin-injected condition: `ROBOT_BLOCKED`, `ROBOT_BATTERY`, `ROBOT_DISCONNECT`,
  `ASRS_UNIT`, `ASRS_NO_READ`.
- **Simulated time** — `simclock.Sim`, advanced only by the engine; `SIM_SPEED` scales it.

## Aggregates and their invariants

- **`robot.Robot`** (richest aggregate; the `mutation-fast` target):
  - A new `orderId` is accepted only when the current order is finished, and its first node must
    be the robot's last node or within `PositionTolerance` (0.5 m) of its pose → else
    `INVALID_ORDER`.
  - An order needs an id, at least one node, exactly one edge between each node pair joining its
    neighbours, a released first node, no released node after the horizon, and only supported
    node action types → else `VALIDATION_FAILURE`.
  - An update with a lower `UpdateID` → `OUTDATED_ORDER_UPDATE`; equal → `SAME_ORDER_UPDATE_ID`;
    it must start at the last released node of the current order → else `VALIDATION_FAILURE`.
  - It drives only the released base, never into the horizon; speed is the configured speed capped
    by an edge `MaxSpeed`. It does not move while blocked, charging, or at 0 % state of charge, and
    stops short of a node when the battery runs dry.
  - Node actions run one at a time; a refused `pick`/`drop` (already loaded, nothing to drop, port
    refuses) fails the action with `LOAD_NOT_AVAILABLE` and holds the robot on that node.
  - `cancelOrder` fails every unfinished action and clears the order but keeps `orderId` and
    `lastNodeId`; with no order it fails with `NO_ORDER_TO_CANCEL`. Any other unknown instant
    action → `INVALID_INSTANT_ACTION`.
  - A new order clears `errors` and `instantActionStates`; both are bounded to the 50 most recent
    (`MaxReported`). `ROBOT_BLOCKED` (`CRITICAL`) and `BATTERY_EMPTY` (`FATAL`) are derived in
    every snapshot while the condition holds. State of charge stays within 0-100.
- **`asrs.System`**:
  - The layout needs positive aisles, levels and slots; every port lies on an existing aisle.
  - `Submit` refuses at once (the reason goes back in the ACK): `UNKNOWN_PORT`, `WRONG_PORT_KIND`,
    `INVALID_POSITION`, `AISLE_MISMATCH` (the port or both positions must share one aisle),
    `DUPLICATE_JOB` (a job id is never reusable, even after it ended).
  - Jobs queue FIFO per aisle unit and run one at a time; duration = `Base + PerLevel×level +
    PerSlot×slot`. When a job starts it is checked again and fails with `CONTAINER_NOT_AT_PORT`,
    `CONTAINER_NOT_AT_POSITION`, `POSITION_OCCUPIED`, `POSITION_BLOCKED` (front of a double-deep
    slot occupied) or `PORT_OCCUPIED`.
  - Every accepted job ends in exactly one `JOB_DONE` or `JOB_FAILED`; a RETRIEVE's `JOB_DONE` is
    followed by `PORT_ARRIVAL`. Only a queued job can be cancelled (`JOB_RUNNING`, `UNKNOWN_JOB`
    otherwise).
  - A faulted unit is `HELD` and keeps its current job until cleared. The no-read fault applies to
    output ports only. A port or rack position holds at most one container.

## Domain events (what the PLC reports upward)

`asrs.Event` kinds: `JOB_DONE`, `JOB_FAILED` (with reason), `UNIT_STATE` (on every state change,
and for every aisle on `STATE_REQUEST`), `PORT_ARRIVAL`. They are MQTT telegrams, not Kafka
events. The robot raises no events: its `Snapshot()` is the content of a VDA 5050 `state` message
and the engine publishes it.

## Application: the engine (`internal/application/engine`)

`engine.Engine` serialises all access to the robots and systems behind one mutex. Its operations
are the emulator's use cases:

- `Step(dt)` — advance simulated time, tick every robot and AS/RS, publish changed or due state
  and the AS/RS events.
- `Announce` / `AnnounceRobot` — publish `ONLINE`, factsheet and state (boot and MQTT reconnect).
- `HandleOrder`, `HandleInstantActions` — VDA 5050 inbound; a fault-disconnected robot refuses
  them (`ErrRobotDisconnected`).
- `SubmitJob`, `CancelJob`, `UnitStates` — telegram inbound.
- `InjectFault`, `Seed`, `PlaceAtPort`, `Robots`, `Asrs` — admin API.

Driven ports: `RobotPublisher` and `AsrsPublisher` (MQTT adapters in `internal/adapters/mqtt`).
`engine.World` builds the clock, robots, systems and the port-node map from the world file.
