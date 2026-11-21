---
id: quickstart
title: Quickstart
sidebar_position: 3
description: "Run the emulator against a local Mosquitto, inspect it and inject a fault."
---

# Quickstart

Needs Go (see `go.mod`) and Docker for the broker.

```bash
docker run --rm -p 1883:1883 eclipse-mosquitto:2.0.20 mosquitto -c /mosquitto-no-auth.conf
WORLD_FILE=config/world.example.yaml SIM_SPEED=10 go run ./cmd/emulator
```

The example world has three robots (`EMU/R1`…`R3`) and one double-deep AS/RS
`ASRS1` with two aisles and an input and an output port per aisle.

## Inspect

```bash
curl -s localhost:8080/readyz -o /dev/null -w '%{http_code}\n'   # 200 once MQTT is connected
curl -s localhost:8080/robots | jq '.items'                     # a {items, nextCursor} page
curl -s localhost:8080/asrs/ASRS1
```

## Watch the wire

```bash
docker run --rm --network host eclipse-mosquitto:2.0.20 mosquitto_sub -v -t 'vda5050/v3/#' -t 'asrs/v1/#'
```

## Seed stock and inject a fault

```bash
curl -s -XPOST localhost:8080/asrs/ASRS1/seeds -H 'content-type: application/json' \
  -d '{"position":{"aisle":1,"level":2,"slot":10,"depth":1},"containerId":"TOTE-1"}'
curl -s -XPOST localhost:8080/faults -H 'content-type: application/json' \
  -d '{"kind":"ROBOT_BLOCKED","target":"R1","active":true}'
```

Both answer `204`. The field names are those of `apis/openapi.yaml`; see the
[API reference](/docs/api-reference).

## Command the devices

Commands go over MQTT only, as a fleet control or WCS would send them: a VDA 5050
`order` to `vda5050/v3/EMU/R1/order`, a `STORE` telegram to `asrs/v1/ASRS1/down`.
The message shapes are on the [MQTT protocols](/docs/api-reference/mqtt) page.
