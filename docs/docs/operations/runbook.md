---
id: runbook
title: Runbook
sidebar_position: 2
description: "Start, check, reset and stop the emulator; what each probe means."
---

# Runbook

The emulator runs in development, CI and the test namespace of the cluster, never
next to real equipment: anything that reaches the admin port can inject faults.

## Probes

| Probe | Answer | Meaning |
| --- | --- | --- |
| `GET /healthz` | `200` | the process is running |
| `GET /readyz` | `200` / `503` problem | `503` until every robot client and every AS/RS client is connected to the broker |

A pod stuck not-ready means the broker at `MQTT_URL` is unreachable: check the
Mosquitto Service and the value of `mqtt.url`.

## Start and stop

- Boot fails fast (non-zero exit, JSON log line `emulator stopped`) without
  `WORLD_FILE`, with an unreadable or invalid world, or with a non-positive
  `SIM_SPEED` / `TICK_INTERVAL`.
- On `SIGTERM` every robot publishes retained `OFFLINE` and the HTTP server shuts down
  within 5 s, inside the Kubernetes default 30 s grace period. A crash leaves the
  broker to publish each robot's retained `CONNECTION_BROKEN` last will.

## Reset the world

There is no reset endpoint and no persistence: restart the process (or
`kubectl rollout restart` the chart's Deployment). The world is
rebuilt from the world file; rack stock must be seeded again.

## Clear faults

Every fault is switched off with the same `POST /faults` body and `"active": false`.
`GET /robots` lists each robot's active error types; `GET /asrs/{asrsId}` shows unit
states (`HELD` while an `ASRS_UNIT` fault is on).

## Change the world on the cluster

Edit the `world:` value, `helm upgrade`; the ConfigMap changes and the pod has to be
restarted to read it.
