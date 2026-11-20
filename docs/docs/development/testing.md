---
id: testing
title: Testing
sidebar_position: 1
description: "The test suites, the local gates and the CI jobs that run them."
---

# Testing

## Suites

| Suite | Where | Run with |
| --- | --- | --- |
| Unit | `internal/domain/{robot,asrs,simclock}`, `internal/application/engine`, `internal/adapters/{vda5050,telegram,mqtt,inbound/http}`, `internal/config`, `cmd/emulator` | `make test` |
| Coverage gate | 90% on `./internal/domain/...` and `./internal/application/...` | `make coverage` |
| BDD (godog) | `features/robot.feature` (a blocked robot stops and reports it; an unblocked robot finishes its order), `features/asrs.feature` (a no-read hides the container; a unit fault holds the job), runner `features/steps_test.go` | `make bdd` |
| Integration | `internal/adapters/mqtt/gateway_integration_test.go`, `-tags=integration`, Mosquitto 2.0.20 via testcontainers (needs Docker only) | `make integration` |
| Architecture fitness | `internal/architecture/` (layering, no in-memory adapters in `cmd/`, no root `.go` files, no auth middleware, plus fleet sensors copied from the template) | `make arch-test` |
| Mutation | gremlins on `./internal/domain/robot` (fast) and `./internal/domain` (full); thresholds measured in `.gremlins.yaml` | `make mutation`, `make mutation-full` |
| Chart tests | `charts/equipment-emulator/tests/render_test.sh`, `test_service_selectors.py` | run each file directly |

## Local gates

```bash
make check-fast   # fmt-check + vet + arch-test + tests of changed packages
make check        # fmt-check vet build lint test (the pre-push hook)
make check-all    # check + coverage + arch-test + bdd
make guide-lint   # the agent guides
```

The docs site builds with `npm ci && npm run build` inside `docs/`. After a change to
`apis/openapi.yaml`, regenerate the REST reference with
`npm run clean-api-docs equipment-emulator && npm run gen-api-docs equipment-emulator`
and commit `docs/docs/api-reference/rest`.

## CI jobs (`.github/workflows/ci.yml`)

`lint`, `guide-lint`, `complexity`, `file-length`, `test`, `bdd`, `integration`,
`mutation-fast`, `api-lint` (Spectral on both specs), `vuln`, `docs-api-drift` (the
generated REST reference matches the spec), `helm-lint` (PRs; `ct lint` plus `helm
lint` with every `ci/*.yaml` values file), `chart-tests`, `arch-test`,
`spec-breaking` (PRs; OpenAPI and AsyncAPI breaking-change gate), `trivy-scan`.
Scheduled or main-only: `mutation` (full), `drift` (weekly advisory; no knip step, the
repo has no `web/`), `docker-publish`, `release`. The `Docs` workflow
(`.github/workflows/docs.yml`) builds the site on every PR that touches `docs/` or
`apis/` and publishes to GitHub Pages from `main`.

The harness behind these sensors is described in
[HARNESS.md](https://github.com/IQVO/equipment-emulator/blob/develop/HARNESS.md); the template's contract-testing and spec-breaking
guides are [docs/contract-testing.md](https://github.com/IQVO/equipment-emulator/blob/develop/docs/contract-testing.md) and
[docs/spec-breaking-kit.md](https://github.com/IQVO/equipment-emulator/blob/develop/docs/spec-breaking-kit.md).
