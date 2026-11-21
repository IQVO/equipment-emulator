---
name: how-to-add-a-rest-endpoint
description: Add or change a REST endpoint in this service in the fleet's hexagonal order (domain invariant, use case, port, HTTP adapter, apis/openapi.yaml, generated docs, godog scenario). Use when touching internal/adapters/inbound/http, apis/openapi.yaml, or exposing a use case over HTTP.
---

# How to add a REST endpoint (the emulator's admin API)

The admin API only inspects and perturbs the simulation; devices are commanded over MQTT, never
over REST. Read `.claude/rules/rest-api.md` first. Work domain first, adapter last.

## 1. Domain: does the simulation already support it?

The rule lives in `internal/domain/robot` (`Robot`) or `internal/domain/asrs` (`System`), with a
table-driven unit test, BEFORE any adapter change. Example: `asrs.System.SetNoRead` refuses an input
port with `WRONG_PORT_KIND` in the domain, not in the handler. No JSON tags in the domain
(`TestDomainHasNoJSONTags`).

## 2. Application: an engine operation

Add a method to `engine.Engine` (`internal/application/engine/engine.go`) that takes `e.mu`, looks
the device up (`e.robot(serial)` / `e.system(id)`, which wrap `ErrUnknownRobot` / `ErrUnknownAsrs`)
and calls the domain method. Publish through the `RobotPublisher` / `AsrsPublisher` ports only.
Test it in `engine_test.go` with the `recorder` publisher (`newEngine(t)`): success path and failure path.

## 3. Adapter: the HTTP handler

In `internal/adapters/inbound/http/router.go`:

1. Add the method to the `Admin` interface (the engine satisfies it).
2. Add a handler on `handlers`: decode the body (400 problem on a bad body or missing field),
   call `Admin`, answer with `writeJSON` or `done` (204, or `commandError`: 404 unknown device,
   409 `asrs.RejectError`, 422 otherwise).
3. Register the route in `NewRouter`. JSON structs live only in this package.
4. Add an httptest in `router_test.go`: one success and one error path.

## 4. Contract

Add the path to `apis/openapi.yaml` (operationId, request/response schemas, `application/problem+json`
for each error) and lint it the way the `api-lint` CI job does (`.spectral.yaml`). The
`spec-breaking` job fails a breaking change against develop; see `docs/spec-breaking-kit.md`.
Update the route list in `.claude/rules/rest-api.md` and the table in `SPEC.md`.

## 5. Behaviour

If the endpoint drives a test scenario (a new fault, a new seed), extend `features/*.feature` and
`features/steps_test.go` (godog, real HTTP against the engine).

## 6. Verify

```bash
make check-fast   # fmt-check + vet + arch-test
make check-all    # + coverage (90% on domain and application) + bdd
```

Never add auth middleware (`TestNoAuthMiddlewareReintroduced`).
