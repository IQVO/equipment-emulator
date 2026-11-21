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
- `GET /robots` -> `Admin.Robots` (`listRobots`; keyset page `{items, nextCursor}`, `limit` + `cursor`)
- `GET /asrs/{asrsId}` -> `Admin.Asrs` (`getAsrs`; 404 for an unknown id)
- `POST /faults` -> `Admin.InjectFault` (`injectFault`; 204)
- `POST /asrs/{asrsId}/seeds` -> `Admin.Seed` (`seedContainer`; 204)
- `POST /asrs/{asrsId}/ports/{portId}/container` -> `Admin.PlaceAtPort` (`placeContainerAtPort`; 204)

## Conventions

- Error shape: RFC 7807 `application/problem+json` (`type: about:blank`, `title`, `status`,
  `detail`, `code`). Every problem type is a catalogue entry in `problems.go` with its replay class;
  answer through `writeError`/`writeProblem` only (they mark RETRYABLE answers for the idempotency
  middleware). 400 for an undecodable body or a missing required field; 404 for an unknown robot or
  AS/RS; 409 for an `asrs.RejectError`, with the bare reason code as `detail`; 422 for an unknown
  fault kind or a result that could not be published. The class table is ADR 0006; a new problem type
  changes both (`TestADR0006ClassTableEqualsTheCatalogue`).
- Every POST is wrapped by the `Idempotency-Key` middleware; the key is OPTIONAL on this API and
  the store is in memory (ADR 0006). Never make it required without a new ADR and a matching
  asrs-control change (its e2e test sends no key).
- Every collection GET pages with `page` (copied verbatim from pricing): `limit` 1-500, opaque
  `cursor`, `{items, nextCursor}`, 400 `unknown-query-parameter`. `api-lint` runs the fleet
  `rest-guideline` Spectral rule (`.spectral.yaml`, `.spectral/functions/restGuideline.js`, byte
  copies of the cohort's).
- Auth: none. The fleet-wide REST auth was reverted 2026-09-11; never add bearer/JWT/API-key
  middleware (`TestNoAuthMiddlewareReintroduced` in `internal/architecture/fitness_test.go`).
- The admin API inspects and perturbs the simulation; it never commands devices. Orders and jobs
  arrive only over MQTT (`apis/asyncapi.yaml`).
