---
id: overview
title: API overview
slug: /api-reference
sidebar_position: 1
description: "The emulator's interfaces: the admin REST API and the two MQTT protocols."
---

# API overview

The emulator has two kinds of interface: the **device protocols** over MQTT, which
the WCS services consume, and the **admin REST API**, which tests and operators use
to inspect the world, inject faults and seed stock. There is no Kafka, MCP or A2A
interface ([ADR 0001](/docs/adr/0001-emulator-role-and-boundaries)).

| Interface | Source of truth | Notes |
| --- | --- | --- |
| REST (admin) | `apis/openapi.yaml` (OpenAPI 3.0.3, version 1.0.0) | The pages under *Equipment Emulator Admin API* in this section are generated from the spec with `npm run gen-api-docs`; do not edit them by hand. Served by `cmd/emulator` on `HTTP_ADDR` (default `:8080`). Unauthenticated (fleet decision 2026-09-11). No Kong route. |
| MQTT | `apis/asyncapi.yaml` (AsyncAPI 2.6.0, version 1.0.0) | Summarised on [MQTT protocols](/docs/api-reference/mqtt); full rules in the [VDA 5050 subset](/docs/reference/vda5050-subset) and the [telegram protocol](/docs/reference/telegram-protocol). |

## Admin endpoints

| Tag | Endpoint | Operation id | Success |
| --- | --- | --- | --- |
| Health | `GET /healthz` | `healthz` | `200` |
| Health | `GET /readyz` | `readyz` | `200`, `503` until the broker is connected |
| Robots | `GET /robots` | `listRobots` | `200` one `{items, nextCursor}` page of robots (order, last node, pose, battery, load, active error types) |
| Asrs | `GET /asrs/{asrsId}` | `getAsrs` | `200` rack slots, port contents, unit states |
| Faults | `POST /faults` | `injectFault` | `204` |
| Asrs | `POST /asrs/{asrsId}/seeds` | `seedContainer` | `204` |
| Asrs | `POST /asrs/{asrsId}/ports/{portId}/container` | `placeContainerAtPort` | `204` |

## Conventions

- Errors are RFC 7807 `application/problem+json` with a stable `code`: `400` an undecodable or
  incomplete body, a malformed key or a bad paging parameter, `404` an unknown robot or AS/RS,
  `409` a device refusal (the `detail` is the bare reason code, e.g. `WRONG_PORT_KIND`,
  `UNKNOWN_PORT`) or a reused key, `422` an unknown fault kind or a result that could not be
  published.
- Fault injection is idempotent by construction: `active` switches a fault on or off.
- Every POST accepts an **optional** `Idempotency-Key`
  ([ADR 0006](/docs/adr/0006-rest-guideline-on-the-admin-api)). With it, a retry is answered
  from the store (`Idempotency-Replayed: true`) when the first answer was a 204 or a final
  refusal; a retryable refusal (`port-occupied`, `position-occupied`, `asrs-rejected`,
  `command-not-applied`) releases the key. Keys are kept 24 hours in process memory and are
  lost on a restart.
- `GET /robots` pages: `limit` (1-500, default 100) and the opaque `cursor` from the
  previous page's `nextCursor`; any other query parameter is `400 unknown-query-parameter`.
- The admin API never commands a device; orders and jobs arrive only over MQTT.
