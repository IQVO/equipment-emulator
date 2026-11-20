---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

# REST API (inbound adapter): the admin API

Router: `internal/adapters/inbound/http/router.go` (chi). It depends on the `Admin` interface,
which `engine.Engine` implements; `cmd/emulator` wires it. Keep this list in sync with
`apis/openapi.yaml` by hand.

- `GET /healthz` -> 200 (liveness)
- `GET /readyz` -> 200, or a 503 problem while the MQTT broker is not connected
- `GET /robots` -> `Admin.Robots` (`listRobots`)
- `GET /asrs/{asrsId}` -> `Admin.Asrs` (`getAsrs`; 404 for an unknown id)
- `POST /faults` -> `Admin.InjectFault` (`injectFault`; 204)
- `POST /asrs/{asrsId}/seeds` -> `Admin.Seed` (`seedContainer`; 204)
- `POST /asrs/{asrsId}/ports/{portId}/container` -> `Admin.PlaceAtPort` (`placeContainerAtPort`; 204)

## Conventions

- Error shape: RFC 7807 `application/problem+json` (`type: about:blank`, `title`, `status`,
  `detail`). 400 for an undecodable body or a missing required field; 404 for an unknown robot or
  AS/RS (`engine.ErrUnknownRobot`, `engine.ErrUnknownAsrs`); 409 for an `asrs.RejectError`, with the
  bare reason code as `detail`; 422 for anything else (e.g. an unknown fault kind).
- Auth: none. The fleet-wide REST auth was reverted 2026-09-11; never add bearer/JWT/API-key
  middleware (`TestNoAuthMiddlewareReintroduced` in `internal/architecture/fitness_test.go`).
- The admin API inspects and perturbs the simulation; it never commands devices. Orders and jobs
  arrive only over MQTT (`apis/asyncapi.yaml`).
