---
id: observability
title: Observability
sidebar_position: 3
description: "What the emulator emits today: structured logs only."
---

# Observability

Today the emulator emits **structured JSON logs** on stdout (`log/slog`,
`slog.NewJSONHandler`) and nothing else: no metrics endpoint and no traces. Adopting
the fleet OpenTelemetry standard is a later wave of the fleet-standard work.

## Log lines worth knowing

| Message | Level | Meaning |
| --- | --- | --- |
| `emulator running` | INFO | boot finished; carries `robots`, `asrs`, `simSpeed` |
| `emulator stopped` | ERROR | boot or the HTTP server failed; the process exits 1 |
| `unsupported order` | WARN | an order with an edge action or a node without `nodePosition` was **dropped**; fleet control gets no feedback ([ADR 0002](/docs/adr/0002-vda5050-v3-robot-side-subset)) |
| `malformed order` / `malformed instantActions` | WARN | the message did not decode; dropped |
| `message on an unexpected topic` | WARN | a message arrived on a robot topic the emulator does not handle |
| `resubscribe failed` / `re-announce failed` | WARN | a reconnect could not restore a robot's subscriptions or announce it |
| `step` | WARN | a simulation step failed to publish |

The wire itself is the best trace: subscribe to `vda5050/v3/#` and `asrs/v1/#` on
the broker (see the [Quickstart](/docs/overview/quickstart)).
