# ADR 0002: VDA 5050 v3 robot side, the implemented subset and its known gap

- Status: Accepted (2026-11-20). Records what `develop` implements (PR #2); the full rules live in
  [`docs/vda5050-subset.md`](https://github.com/IQVO/equipment-emulator/blob/develop/docs/vda5050-subset.md) and the
  wire contract in `apis/asyncapi.yaml`.
- Builds on [ADR 0001](/docs/adr/0001-emulator-role-and-boundaries).

## Context

`amr-fleet-control` is a VDA 5050 master control. To test it without robots, the emulator has to be the robot
side of the protocol: accept orders and instant actions, drive, run actions and publish state. Full VDA 5050 v3.0.0
conformance (trajectories, zones, maps, visualization, responses) is far more than a fleet-control test needs,
so the emulator implements a subset and names what it leaves out.

## Decision

1. **Transport.** One MQTT 3.1.1 client per robot, QoS 1, topics `vda5050/v3/<manufacturer>/<serialNumber>/<topic>`,
   every message with the VDA 5050 header (`headerId` per topic, `timestamp`, `version`, `manufacturer`,
   `serialNumber`) (`internal/adapters/mqtt/robot_gateway.go`, `internal/adapters/vda5050/messages.go`).
2. **Topics implemented.** `order` and `instantActions` (received), `state`, `connection` and `factsheet`
   (published). `connection` is retained `ONLINE`/`OFFLINE` with a retained `CONNECTION_BROKEN` last will; after a
   reconnect the robot subscribes again and re-announces `ONLINE`, the factsheet and the state. `state` is
   published on change, at once after every order and instant-action message, and at least every 30 s of
   simulated time (§6.6) (`internal/application/engine/engine.go`, `publishState`).
3. **Orders.** Orders and order updates stitched at the last released node; the robot drives the released base
   node by node at its configured speed, capped by an edge's `maxSpeed` (`internal/domain/robot/order.go`,
   `robot.go`). The acceptance rules (`INVALID_ORDER`, `OUTDATED_ORDER_UPDATE`, `SAME_ORDER_UPDATE_ID`,
   `VALIDATION_FAILURE`) are reported as `WARNING` errors in the next `state`; there is no reply topic.
4. **Actions.** Node actions `pick` (`loadId`), `drop`, `startCharging`, `stopCharging`, one at a time for the
   configured action duration; instant actions `cancelOrder`, `startCharging`, `stopCharging`, `stateRequest`,
   `factsheetRequest`. A `pick` or `drop` at a node that the world file maps to an AS/RS port reaches the AS/RS
   ([ADR 0003](/docs/adr/0003-asrs-telegram-protocol-v1)) through `internal/application/engine/engine.go`
   (`peripherals`).
5. **Emulator error types** added to VDA 5050's open `errorType` enumeration: `LOAD_NOT_AVAILABLE` (`WARNING`),
   `ROBOT_BLOCKED` (`CRITICAL`), `BATTERY_EMPTY` (`FATAL`).
6. **Left out on purpose.** Trajectories, corridors and zones (`zoneSet`), the `visualization` topic, `responses`,
   map download. `blockingType` is accepted and ignored (actions run sequentially), `paused` is always `false`,
   `HIBERNATING` is never published, `operatingMode` is always `AUTOMATIC`, `safetyState` never reports an
   emergency stop or field violation, the factsheet carries only `typeSpecification` and `physicalParameters`
   (`internal/adapters/vda5050/mapping.go`, `FromState`, `FromFactsheet`). `errors` and `instantActionStates` keep
   the 50 most recent entries (`internal/domain/robot/types.go`, `MaxReported`).

## Known gap: edge actions are dropped without feedback

`vda5050.ToOrder` (`internal/adapters/vda5050/mapping.go`) refuses an order that carries any edge action, or a node
without `nodePosition`. The robot gateway logs `unsupported order` and drops the message
(`internal/adapters/mqtt/robot_gateway.go`); the domain never sees it, so nothing is reported in `state`. Fleet
control gets **no feedback** for such an order: it waits for a state change that never comes.

This is accepted for now because `amr-fleet-control` sends no edge actions and every node of its map carries a
position. The fix, when a consumer needs it, is to map the refusal to a `VALIDATION_FAILURE` `WARNING` in the next
`state` like the other rejections. That change alters what the wire reports and gets its own ADR.

## Consequences

- Fleet-control tests exercise the order, state and connection lifecycle of real VDA 5050 v3 against the subset.
  Anything outside it (zones, trajectories, pause, emergency stop) cannot be tested against the emulator.
- The subset is a contract: `apis/asyncapi.yaml` is Spectral-linted and gated by the `spec-breaking` job; a change
  to the subset updates `docs/vda5050-subset.md`, the AsyncAPI document and, if behaviour changes, a new ADR.
