---
id: introduction
title: Introduction
sidebar_position: 1
description: "What the equipment emulator is, what it simulates and what it deliberately is not."
---

# Introduction

The equipment emulator stands in for **ISA-95 levels 1 and 0** of the WCS tier: the
robots and the AS/RS PLC that this study project does not have. It lets the WCS
subsystem controllers, `amr-fleet-control` and `asrs-control`, be built and tested
against a real wire protocol without hardware.

:::info Test infrastructure, not a bounded context
The WCS strategic design (warehouse-docs `docs/wcs/implementation-handoff.md`,
"Emulator scope") classes equipment emulation as **not a domain**. The emulator owns
no business data, holds no business rule, publishes no Kafka event and has no
database. See [ADR 0001](/docs/adr/0001-emulator-role-and-boundaries).
:::

## What it simulates

| Device | Protocol | Transport | Decision |
| --- | --- | --- | --- |
| AMR robots | Robot side of VDA 5050 v3.0.0 (a documented subset) | MQTT 3.1.1, QoS 1, JSON | [ADR 0002](/docs/adr/0002-vda5050-v3-robot-side-subset), [VDA 5050 subset](/docs/reference/vda5050-subset) |
| AS/RS PLC | AS/RS telegram protocol v1 (the emulator's own; `asrs-control` conforms to it) | MQTT 3.1.1, QoS 1, JSON | [ADR 0003](/docs/adr/0003-asrs-telegram-protocol-v1), [Telegram protocol](/docs/reference/telegram-protocol) |

Next to the devices it serves a small **admin REST API** that inspects the simulated
world, injects faults and seeds stock ([ADR 0004](/docs/adr/0004-simulated-time-fault-injection-and-admin-api)).
It never commands a device: orders and jobs arrive only over MQTT, as on a shop floor.

## What it is not

The fleet surfaces every bounded context carries do not apply here and are absent
by decision:

- **No Kafka, no CloudEvents, no outbox.** Nothing the emulator does is a business fact.
- **No Postgres.** All state is in memory and lost on restart; the world comes from a
  YAML file at boot.
- **No MCP server, no A2A agent.** Agents work with the WCS contexts, not with the
  simulated devices.
- **No Kong / Gateway API route.** The admin API is reached inside the cluster or by
  port-forward; it is unauthenticated (fleet decision 2026-09-11).
- **No DDD artifact pack.** There is no domain model to describe; the device models
  in `internal/domain/{robot,asrs,simclock}` are simulations.
- **No business rules.** Slotting, routing, traffic control and scheduling belong to
  `asrs-control`, `amr-fleet-control` and `material-flow-control`.

## Where to go next

- [Architecture](/docs/overview/architecture): the binary, its layers and the simulation loop.
- [Quickstart](/docs/overview/quickstart): run it against a local Mosquitto and drive it.
- [MQTT protocols](/docs/api-reference/mqtt) and the generated [admin REST API](/docs/api-reference).
- [Testing](/docs/development/testing): unit, BDD, integration and mutation suites, and the CI jobs.
