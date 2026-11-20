---
id: configuration
title: Configuration
sidebar_position: 1
description: "Environment variables, the world file and the Helm values."
---

# Configuration

`cmd/emulator` reads its environment in `internal/config/config.go` and exits at boot
on an invalid value.

| Variable | Default | Meaning |
| --- | --- | --- |
| `WORLD_FILE` | required | YAML world: robots and AS/RS systems (`config/world.example.yaml`) |
| `MQTT_URL` | `tcp://localhost:1883` | MQTT broker for every robot and AS/RS client |
| `HTTP_ADDR` | `:8080` | Admin API listen address |
| `SIM_SPEED` | `1` | Simulated seconds per wall-clock second; must be positive |
| `TICK_INTERVAL` | `100ms` | Wall-clock interval between simulation ticks; must be a positive duration |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty | OTLP/gRPC Collector endpoint (host:port or URL); empty turns telemetry off |
| `OTEL_SERVICE_NAME` | `equipment-emulator` | `service.name` of every span and metric |
| `SERVICE_VERSION` | `dev` | `service.version` |
| `ENVIRONMENT` | `local` | `deployment.environment.name` |

The four observability variables are read by `cmd/emulator/telemetry.go` and the
telemetry adapter ([Observability](/docs/operations/observability)).

## World file

```yaml
robots:
  - {manufacturer: EMU, serialNumber: R1, startX: 0, startY: 0, mapId: S1, speed: 1.5,
     drainPerMeter: 0.02, chargePerSecond: 0.5, actionSeconds: 3}
asrs:
  - id: ASRS1
    aisles: 2
    levels: 8
    slots: 40
    doubleDeep: true
    baseSeconds: 6
    perLevelSeconds: 1.5
    perSlotSeconds: 0.2
    ports:
      - {id: IN1, aisle: 1, kind: INPUT, nodeId: ASRS1-IN1}
      - {id: OUT1, aisle: 1, kind: OUTPUT, nodeId: ASRS1-OUT1}
```

- `robots[]`: identity (manufacturer and serial number form the MQTT topic), start
  pose and map, speed in m/s, battery drain per metre, charge per second, action
  duration in seconds.
- `asrs[]`: rack dimensions, double-deep, job timing (job time = base + per-level ×
  level + per-slot × slot), and ports. A port's `nodeId` is the VDA 5050 node where
  robots drop at or pick from it; `amr-fleet-control`'s map must use the same ids.
- `asrs-control` reads the same field names from its own `ASRS_LAYOUT_FILE` and ignores
  the emulator-only timing and `nodeId` fields (its ADR 0002).

## Helm values

The chart `charts/equipment-emulator/` renders the environment into a ConfigMap
mounted with `envFrom` and the `world:` value into `world.yaml`, mounted at
`config.worldFile` (`/etc/emulator/world.yaml`).

| Value | Default | Becomes |
| --- | --- | --- |
| `mqtt.url` | `tcp://mosquitto.warehouse-systems.svc.cluster.local:1883` | `MQTT_URL` |
| `config.httpAddr` | `:8080` | `HTTP_ADDR` |
| `config.simSpeed` | `"1"` | `SIM_SPEED` |
| `config.tickInterval` | `100ms` | `TICK_INTERVAL` |
| `config.worldFile` | `/etc/emulator/world.yaml` | `WORLD_FILE` |
| `world` | three robots, one AS/RS | `world.yaml` in the ConfigMap |
| `otel.enabled` | `true` | renders the two `OTEL_*` variables below |
| `otel.endpoint` | `otel-collector.observability.svc.cluster.local:4317` | `OTEL_EXPORTER_OTLP_ENDPOINT` |
| `otel.serviceName` | `equipment-emulator` | `OTEL_SERVICE_NAME` |
| `serviceVersion` | empty (`image.tag`, then `appVersion`) | `SERVICE_VERSION` |
| `environment` | `production` | `ENVIRONMENT` |
| `extraEnv` | `[]` | extra container env entries; the four observability names are refused |

There are no database, Kafka, MCP, A2A or gateway values: the emulator has none of
those surfaces ([ADR 0001](/docs/adr/0001-emulator-role-and-boundaries)).
