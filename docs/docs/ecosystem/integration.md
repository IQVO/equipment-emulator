---
id: integration
title: Integration
sidebar_position: 1
description: "Who talks to the emulator, over what, and which fleet surfaces do not apply."
---

# Integration

The emulator sits below the WCS tier as a simulated device. It calls no service; its
peers connect to the same MQTT broker.

| Peer | Relationship | Edge |
| --- | --- | --- |
| `amr-fleet-control` | VDA 5050 master control; the emulator is the robot side | MQTT `vda5050/v3/...` ([ADR 0002](/docs/adr/0002-vda5050-v3-robot-side-subset)) |
| `asrs-control` | Conformist to telegram protocol v1 (WCS context map relationship 7) | MQTT `asrs/v1/<asrsId>/down` and `/up` ([ADR 0003](/docs/adr/0003-asrs-telegram-protocol-v1)) |
| tests, e2e harness, operators | inspect, seed, inject faults | admin REST ([ADR 0004](/docs/adr/0004-simulated-time-fault-injection-and-admin-api)) |
| Mosquitto (WCS broker) | transport | `MQTT_URL` |

The AS/RS layout is shared by convention, not by a call: `asrs-control`'s
`ASRS_LAYOUT_FILE` uses the field names of the emulator's world file, and
`amr-fleet-control`'s map must use the port `nodeId`s of the world file. Seeding the
robot map from `facility-layout`'s travel graph is planned, not built.

## Fleet surfaces that do not apply

The emulator is not a bounded context ([ADR 0001](/docs/adr/0001-emulator-role-and-boundaries)),
so the following fleet standards are **not applied, by decision**:

| Surface | Why not |
| --- | --- |
| Kafka, CloudEvents, outbox | no business fact to publish; the devices speak MQTT |
| Postgres, migrations | all state is simulated and in memory |
| MCP server, A2A agent | agents work with the WCS contexts, not with simulated devices |
| Kong / Gateway API route | the admin API is internal test tooling |
| DDD artifact pack | no domain model; the device models are simulations |
| Auth | no fleet service has an auth layer (fleet decision 2026-09-11) |

Deployment through `warehouse-infra` next to the WCS Mosquitto broker is planned,
not yet registered.
