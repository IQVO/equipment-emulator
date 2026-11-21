---
id: mqtt
title: MQTT protocols
sidebar_position: 50
description: "The VDA 5050 v3 and AS/RS telegram v1 channels of apis/asyncapi.yaml."
---

# MQTT protocols

Both protocols run over MQTT 3.1.1 at QoS 1 with JSON (UTF-8) payloads. The contract
is [`apis/asyncapi.yaml`](https://github.com/IQVO/equipment-emulator/blob/develop/apis/asyncapi.yaml) (AsyncAPI 2.6.0), Spectral-linted
by `api-lint` and gated by `spec-breaking`. In the AsyncAPI document operations are
written from the emulator's point of view: `publish` means the peer publishes and the
emulator receives.

## VDA 5050 v3 (robots)

Topic prefix `vda5050/v3/<manufacturer>/<serialNumber>/`, one MQTT client per robot.

| Channel | Direction | Retained | Content |
| --- | --- | --- | --- |
| `order` | fleet control → robot | no | orders and order updates |
| `instantActions` | fleet control → robot | no | `cancelOrder`, `startCharging`, `stopCharging`, `stateRequest`, `factsheetRequest` |
| `state` | robot → fleet control | no | on change, after every received message, at least every 30 s simulated |
| `connection` | robot → fleet control | yes | `ONLINE`, `OFFLINE`, `CONNECTION_BROKEN` (last will) |
| `factsheet` | robot → fleet control | yes | `typeSpecification`, `physicalParameters` |

Rejections appear as `WARNING` errors in the next `state`. Orders with edge actions
are dropped without feedback (the known gap of
[ADR 0002](/docs/adr/0002-vda5050-v3-robot-side-subset)). Full rules:
[VDA 5050 subset](/docs/reference/vda5050-subset).

## AS/RS telegram protocol v1 (PLC)

| Channel | Direction | Telegrams |
| --- | --- | --- |
| `asrs/v1/<asrsId>/down` | WCS → PLC | `STORE`, `RETRIEVE`, `MOVE`, `CANCEL`, `STATE_REQUEST`, ACKs of up telegrams |
| `asrs/v1/<asrsId>/up` | PLC → WCS | `ACK`, `JOB_DONE`, `JOB_FAILED`, `UNIT_STATE`, `PORT_ARRIVAL` |

Every non-ACK telegram is acknowledged; an unacknowledged up telegram is retransmitted
after 2 s (real time), at most 3 times; a repeated `telegramId` gets the first ACK
again. `asrs-control` conforms to this protocol (its ADR 0002). Full rules:
[telegram protocol](/docs/reference/telegram-protocol) and
[ADR 0003](/docs/adr/0003-asrs-telegram-protocol-v1).

A STORE telegram:

```json
{"telegramId": "6f1c...", "type": "STORE", "timestamp": "2026-11-20T10:00:00.000Z",
 "jobId": "J-1", "containerId": "TOTE-1", "portId": "IN1",
 "to": {"aisle": 1, "level": 2, "slot": 10, "depth": 1}}
```
