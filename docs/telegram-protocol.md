# AS/RS telegram protocol v1

The PLC side of the emulated AS/RS publishes this protocol, the way an AS/RS
vendor would. `asrs-control` conforms to it (relationship 7 on the WCS context
map). No public standard exists for this interface; the shape follows SAP EWM's
telegrams and handshake: every telegram is acknowledged, unacknowledged
telegrams are retransmitted, and receivers deduplicate.

## Transport

MQTT 3.1.1, QoS 1, JSON, UTF-8. Not retained.

| Topic | Direction |
| --- | --- |
| `asrs/v1/<asrsId>/down` | WCS → PLC: STORE, RETRIEVE, MOVE, CANCEL, STATE_REQUEST, and ACKs of up telegrams |
| `asrs/v1/<asrsId>/up` | PLC → WCS: ACK, JOB_DONE, JOB_FAILED, UNIT_STATE, PORT_ARRIVAL |

## Telegram

One flat JSON object. Fields not used by a type are omitted.

| Field | Type | Used by |
| --- | --- | --- |
| `telegramId` | string (UUID) | all |
| `type` | string | all |
| `timestamp` | string, RFC 3339 UTC with milliseconds | all |
| `jobId` | string | STORE, RETRIEVE, MOVE, CANCEL, JOB_DONE, JOB_FAILED |
| `containerId` | string | STORE, RETRIEVE, MOVE, PORT_ARRIVAL (absent = no-read) |
| `portId` | string | STORE (input port), RETRIEVE (output port), PORT_ARRIVAL |
| `from` | position | RETRIEVE, MOVE |
| `to` | position | STORE, MOVE |
| `ackTelegramId` | string | ACK |
| `accepted` | boolean | ACK |
| `reason` | string | ACK (when not accepted), JOB_FAILED |
| `unitId` | string, `A<aisle>` | UNIT_STATE |
| `state` | `IDLE` \| `EXECUTE` \| `HELD` (PackML names) | UNIT_STATE |

A position is `{"aisle": 1, "level": 2, "slot": 10, "depth": 1}`; `depth` is 1
(front) or 2 (back, double-deep racks only).

## Handshake

1. The receiver of any telegram other than ACK answers with an ACK whose
   `ackTelegramId` is the received `telegramId`. `accepted: false` with a
   `reason` means the command was refused at once (`UNKNOWN_PORT`,
   `WRONG_PORT_KIND`, `INVALID_POSITION`, `DUPLICATE_JOB`, `AISLE_MISMATCH`,
   `UNKNOWN_JOB`, `JOB_RUNNING`, `MALFORMED`).
2. A sender that receives no ACK within **2 s** retransmits the same telegram
   (same `telegramId`), at most **3** times, then gives up and logs it.
3. A receiver that sees a `telegramId` again does not process it again; it
   resends the ACK it sent the first time.

## Job outcomes

An accepted job ends in exactly one JOB_DONE or JOB_FAILED. JOB_FAILED reasons
found when the job starts: `CONTAINER_NOT_AT_PORT`, `CONTAINER_NOT_AT_POSITION`,
`POSITION_OCCUPIED`, `POSITION_BLOCKED` (the front position of a double-deep
slot is occupied), `PORT_OCCUPIED`. A cancelled queued job produces no
JOB_FAILED; the ACK of the CANCEL is the outcome. A RETRIEVE's JOB_DONE is
followed by a PORT_ARRIVAL at its output port.

## Emulator simplifications

One combined shuttle-and-lift unit per aisle; jobs run one at a time per aisle
in FIFO order; job time = base + per-level × level + per-slot × slot.
