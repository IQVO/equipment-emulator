# VDA 5050 v3.0.0 subset

The emulated robots speak the part of VDA 5050 v3.0.0 (JSON over MQTT) listed
here. The wire contract is `apis/asyncapi.yaml`. Topics are
`vda5050/v3/<manufacturer>/<serialNumber>/<topic>`, QoS 1; every message
carries the header (`headerId` counting per topic, `timestamp`, `version`,
`manufacturer`, `serialNumber`). Required arrays serialise as `[]`.

Timestamps are simulated time; at `SIM_SPEED` 1 they track wall time.
Simulated time starts at the process start. Every `state`, `connection` and
`factsheet` message is stamped with it, except the static `CONNECTION_BROKEN`
last will: the broker stores it when the robot's client connects, so it
carries the process-start timestamp.

## Implemented

| Topic | Direction | Notes |
| --- | --- | --- |
| `order` | fleet control to robot | orders and order updates |
| `instantActions` | fleet control to robot | see below |
| `state` | robot to fleet control | published when it changes and at least every 30 s of simulated time (§6.6) |
| `connection` | robot to fleet control | retained; `ONLINE`, `OFFLINE`, `CONNECTION_BROKEN` |
| `factsheet` | robot to fleet control | retained; subset (`typeSpecification`, `physicalParameters`) |

Connection: each robot has its own MQTT client with a retained
`CONNECTION_BROKEN` last will on `connection`. It publishes retained `ONLINE`
after connecting, retained `OFFLINE` on a coordinated shutdown, and after a
reconnect it subscribes again and re-announces `ONLINE`, the factsheet and the
state.

State: the engine publishes a robot's state when it changed since the last
publish, at once after every order and instant-action message, and otherwise
whenever 30 s of simulated time have passed since that robot's last state (VDA
5050 v3 §6.6: "at least every 30 seconds"; at `SIM_SPEED` 10 that is every 3 s
of wall time). A fault-disconnected robot publishes nothing.

Node actions: `pick` (parameter `loadId`), `drop`, `startCharging`,
`stopCharging`. Instant actions: `cancelOrder`, `startCharging`,
`stopCharging`, `stateRequest`, `factsheetRequest`. Action progress is reported
in `actionStates` and `instantActionStates` (`WAITING`, `RUNNING`, `FINISHED`,
`FAILED`).

`nodePosition` is required on every node.

## Order rules

- A new order (new `orderId`) is accepted only when the previous order is
  finished, and its first node must be the robot's `lastNodeId` or lie within
  0.5 m of the robot. Otherwise the error is `INVALID_ORDER`.
- An update (same `orderId`) with a lower `orderUpdateId` is rejected with
  `OUTDATED_ORDER_UPDATE`, with an equal one with `SAME_ORDER_UPDATE_ID`.
- An update must start at the last released node of the current order.
- An order needs an `orderId`, at least one node, one edge between each pair of
  nodes, a released first node and no released node after the horizon; node
  action types must be supported. Otherwise the error is `VALIDATION_FAILURE`.
- There is no reply topic. A rejection by these rules (`OUTDATED_ORDER_UPDATE`,
  `SAME_ORDER_UPDATE_ID`, `INVALID_ORDER`, `VALIDATION_FAILURE`) is reported as
  a `WARNING` error in the next `state`. An unsupported order (any edge action,
  or a node without `nodePosition`) is dropped and logged by the emulator and
  is not reported in `state`: fleet control gets no feedback. `cancelOrder` keeps `orderId` and `lastNodeId`; without an
  active order it fails with `NO_ORDER_TO_CANCEL`. An unsupported instant
  action fails with `INVALID_INSTANT_ACTION`.

## Emulator-specific error types

`errorType` is an open enumeration in VDA 5050. The emulator adds:

| `errorType` | Level | Cause |
| --- | --- | --- |
| `LOAD_NOT_AVAILABLE` | `WARNING` | a `pick` or `drop` the world refused (load missing, already carrying, nothing to drop) |
| `ROBOT_BLOCKED` | `CRITICAL` | an obstacle was injected through the admin API; the robot stops until it is cleared |
| `BATTERY_EMPTY` | `FATAL` | the state of charge reached 0 |

## Not implemented

- Edge actions: an order with one is not supported; the whole order is dropped, logged, not reported in state
- Trajectories, corridors and zones (`zoneSet`)
- `visualization` topic and `responses`
- Maps download
- `blockingType` is accepted but ignored: actions run sequentially
- `paused` is always `false`: there is no pause
- `HIBERNATING` is never published
- The factsheet is a subset: only `typeSpecification` and `physicalParameters`
- `operatingMode` is always `AUTOMATIC`, `safetyState` always reports no
  emergency stop and no field violation
