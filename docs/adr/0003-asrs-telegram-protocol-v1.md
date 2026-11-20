# ADR 0003: AS/RS telegram protocol v1, the PLC model and the handshake

- Status: Accepted (2026-11-20). Records the protocol `develop` implements (PR #2); the full wire rules live in
  [`docs/telegram-protocol.md`](https://github.com/IQVO/equipment-emulator/blob/develop/docs/telegram-protocol.md)
  and the wire contract in `apis/asyncapi.yaml`.
- Builds on [ADR 0001](/docs/adr/0001-emulator-role-and-boundaries).
- Companion: IQVO/asrs-control ADR 0002 "Telegram conformance, the layout file and one job per aisle unit", which
  records how asrs-control conforms to this protocol (relationship 7 on the WCS context map: asrs-control is
  Conformist, the emulated PLC is upstream).

## Context

There is no public standard for the interface between an AS/RS controller and its PLC; every vendor publishes its
own telegrams. `asrs-control` needs one fixed protocol to conform to, and the emulator plays the vendor. The shape
follows SAP EWM's telegrams and handshake: every telegram is acknowledged, unacknowledged telegrams are
retransmitted, receivers deduplicate.

## Decision

1. **Transport.** MQTT 3.1.1, QoS 1, JSON (UTF-8), not retained. `asrs/v1/<asrsId>/down` carries WCS → PLC
   telegrams (`STORE`, `RETRIEVE`, `MOVE`, `CANCEL`, `STATE_REQUEST` and ACKs of up telegrams); `asrs/v1/<asrsId>/up`
   carries PLC → WCS telegrams (`ACK`, `JOB_DONE`, `JOB_FAILED`, `UNIT_STATE`, `PORT_ARRIVAL`)
   (`internal/adapters/mqtt/asrs_gateway.go`).
2. **Telegram.** One flat JSON object; fields a type does not use are omitted; `timestamp` is RFC 3339 UTC with
   milliseconds and carries simulated time on up telegrams (`internal/adapters/telegram/telegram.go`).
3. **Handshake** (`internal/adapters/telegram/handshake.go`):
   - every non-ACK telegram is answered with an ACK whose `ackTelegramId` is the received `telegramId`;
     `accepted: false` carries one bare reason code (`UNKNOWN_PORT`, `WRONG_PORT_KIND`, `INVALID_POSITION`,
     `DUPLICATE_JOB`, `AISLE_MISMATCH`, `UNKNOWN_JOB`, `JOB_RUNNING`, `MALFORMED`, `NOT_READY`);
   - an unacknowledged up telegram is retransmitted after **2 s of real time**, at most **3** times
     (`NewRetransmitter(2*time.Second, 3)`), then given up and logged;
   - a repeated `telegramId` is not processed again; the first ACK is resent (`Dedup`, the last 1000 ids);
   - `NOT_READY` (the engine is not built yet, `cmd/emulator/proxy.go`) is transient and not remembered, so a
     retransmission is processed afresh.
4. **PLC model** (`internal/domain/asrs/`): rack positions `aisle/level/slot/depth`, depth 2 only in double-deep
   racks and blocked while its front position is occupied; one combined shuttle-and-lift unit per aisle runs jobs
   one at a time in FIFO order; job time = base + per-level × level + per-slot × slot (simulated seconds); unit
   states use the PackML names `IDLE`, `EXECUTE`, `HELD`.
5. **Job outcomes.** An accepted job ends in exactly one `JOB_DONE` or `JOB_FAILED` (`CONTAINER_NOT_AT_PORT`,
   `CONTAINER_NOT_AT_POSITION`, `POSITION_OCCUPIED`, `POSITION_BLOCKED`, `PORT_OCCUPIED`, checked when the job
   starts). A RETRIEVE's `JOB_DONE` is followed by a `PORT_ARRIVAL` at its output port; a missing `containerId`
   is a no-read. A queued job can be cancelled (the ACK of the CANCEL is the outcome); a running one cannot
   (`JOB_RUNNING`).
6. **Contract decisions fixed before asrs-control bound to the protocol.** Input ports emit no `PORT_ARRIVAL`. A
   `jobId` is never reusable within the process lifetime (`DUPLICATE_JOB`, also after the job ended). The no-read
   fault applies to output ports only.

## Consequences

- asrs-control implements its own codec and retransmitter that mirror this one and keeps one job per aisle in
  flight (its ADR 0002); the emulator's FIFO queue per aisle is therefore rarely more than one deep in practice.
- Protocol v1 has **no job-state query**. A `JOB_DONE` the PLC gave up retransmitting while the WCS was down for
  longer than about 8 s (2 s × (1 + 3)) is lost; reconciliation needs a protocol v2 with its own ADR here and a
  companion ADR in asrs-control.
- The handshake parameters (2 s, 3 attempts, the reason codes) are contract: changing one is a protocol version
  change, not a tuning knob, and is gated by the AsyncAPI `spec-breaking` job.
