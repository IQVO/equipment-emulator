# ADR 0006: The fleet REST guideline on the admin API (optional Idempotency-Key, keyset paging)

- Status: Accepted (2026-11-20). Applies the fleet REST API guideline (warehouse-docs
  `docs/standards/rest-api-guideline.md`, rules R1-R9) and the idempotency replay classes
  (warehouse-harness-template `docs/idempotency-replay-classes.md`) to the admin API of
  [ADR 0004](/docs/adr/0004-simulated-time-fault-injection-and-admin-api). Two deviations from the fleet are recorded
  here: the key is optional, and the store is in memory.

## Context

The admin API (`apis/openapi.yaml`) had three POSTs without `Idempotency-Key` and a `GET /robots` that answered a bare
array. `api-lint` ran Spectral's OAS rules only, so the fleet guideline never applied. The fleet's keyed routes store
answers in Postgres next to the business transaction. The emulator has no database, no Kafka and no unit of work
([ADR 0001](/docs/adr/0001-emulator-role-and-boundaries)), and it is test infrastructure: asrs-control's end-to-end
test (`cmd/api/e2e_emulator_integration_test.go`, which pins an emulator commit) calls `POST /faults` and
`POST /asrs/{asrsId}/ports/{portId}/container` without a key.

## Decision

1. **Spectral rule.** `.spectral.yaml` and `.spectral/functions/restGuideline.js` are byte copies of pricing's and
   customer-returns' (the cohort's ruleset). `api-lint` runs them unchanged.
2. **`Idempotency-Key` on the three POSTs, OPTIONAL.** All three POSTs declare the header (R3) and document
   `Idempotency-Replayed` on their 204 (R5). Unlike the rest of the fleet, a request without the key runs unkeyed and
   is never refused. Every admin command already converges when it is repeated (a fault switches with `active`, a
   seed or a placement is refused once done), and requiring the key would break every caller that sends none,
   starting with asrs-control's e2e test. A key that is sent is validated (1-128 printable ASCII characters, no
   leading or trailing space; otherwise 400 `idempotency-key-invalid`). A key reused with another method, path or
   body is 409 `idempotency-key-reused`.
3. **The middleware** (`internal/adapters/inbound/http/idempotency.go`) follows asrs-control's shape: capture the
   answer, store a 2xx and a FINAL refusal, release the key on a RETRYABLE refusal (marked by `writeProblem`), a 5xx
   or a panic, and replay with `Idempotency-Replayed: true`. It has no unit of work. A concurrent duplicate waits on
   the key, then replays. The window is 24 hours.
4. **In-memory store, by decision.** `keyStore` (`idempotency_store.go`) lives inside the inbound adapter, not in
   `internal/adapters/outbound/memory`. The fleet's memory adapters are test doubles, and
   `TestRuntimeBinaryHasNoInMemoryAdapters` keeps them out of `cmd/`. This store is the production store, because the
   emulator has nothing durable to put it in. The trade-offs:
   - **The replay window is lost on restart.** A retry that reaches a restarted emulator executes again. Every admin
     command converges, so the cost is one more execution, never a double effect.
   - **Single replica only.** Two replicas would not share keys, which is one more reason (besides the simulated
     world itself, ADR 0001) that the chart runs one replica.
   - **Memory is bounded by one window of admin traffic.** Each save purges every answer older than 24 hours.
5. **Replay classes.** There is no OUTCOME: no refusal commits state. A ROBOT_DISCONNECT whose broker publish failed
   has set the flag, but running it again converges. Ids of robots, AS/RS, ports and rack positions come from the
   world file and never change while the process runs, so a refusal about them is FINAL. An occupied port or
   position changes as robots and jobs move, so it is RETRYABLE. Each problem body now carries `code`, the slug
   below. `type` stays `about:blank` and the titles do not change. `TestADR0006ClassTableEqualsTheCatalogue` holds
   this table equal to the catalogue in `problems.go`:

   | Slug | Status | Class | Answered when |
   | --- | --- | --- | --- |
   | `bad-request` | 400 | FINAL | malformed body, a required field missing, a keyed body over 64 KiB |
   | `idempotency-key-invalid` | 400 | FINAL | the key is sent but malformed, or sent twice |
   | `idempotency-key-reused` | 409 | FINAL | the key was used with another method, path or body |
   | `unknown-query-parameter` | 400 | FINAL | `GET /robots` with a parameter other than `limit`/`cursor` |
   | `invalid-limit` | 400 | FINAL | `limit` outside 1-500, empty or repeated |
   | `invalid-cursor` | 400 | FINAL | an undecodable or repeated `cursor` |
   | `robot-not-found` | 404 | FINAL | a fault targets an unknown robot serial |
   | `asrs-not-found` | 404 | FINAL | an unknown AS/RS id |
   | `unknown-fault-kind` | 422 | FINAL | `kind` is not one of the five fault kinds |
   | `unknown-port` | 409 | FINAL | AS/RS reject reason UNKNOWN_PORT |
   | `wrong-port-kind` | 409 | FINAL | AS/RS reject reason WRONG_PORT_KIND |
   | `invalid-position` | 409 | FINAL | AS/RS reject reason INVALID_POSITION (outside the rack, or an unknown aisle) |
   | `port-occupied` | 409 | RETRYABLE | AS/RS reject reason PORT_OCCUPIED: a robot may take the container away |
   | `position-occupied` | 409 | RETRYABLE | AS/RS reject reason POSITION_OCCUPIED: a retrieve may empty it |
   | `asrs-rejected` | 409 | RETRYABLE | any other AS/RS reject reason (none reachable from an admin route today; unsure means RETRYABLE) |
   | `command-not-applied` | 422 | RETRYABLE | the command's result could not be published to the broker |
   | `not-ready` | 503 | RETRYABLE | `GET /readyz` before every MQTT client is connected (no key on that route) |

   Releasing a key means that the same key with another body afterwards is a new request, not
   `idempotency-key-reused`. `docs/idempotency-replay-classes.md` and the template test
   (`idempotency_replay_classes_test.go`, rendered from warehouse-harness-template with the route `/faults`) apply
   unchanged. The wiring file counts writes in memory, because there is no transaction to commit.
6. **Keyset paging on `GET /robots`** (R7), using pricing's `page` package byte for byte: `limit` (1-500, default
   100) plus an opaque `cursor`, and the answer `{items, nextCursor}` ordered by serial number, with `nextCursor`
   absent on the last page. Any other query parameter is 400 `unknown-query-parameter`. **This changes the wire:**
   the 200 body was a bare array. No repository pins this spec (no `contracts/pinned.json` names it) and no fleet
   code reads `GET /robots`. asrs-control calls only the two POSTs, e2e-tests calls nothing, and amr-fleet-control does not exist yet.
   oasdiff still reports `response-body-type-changed`, so the non-required `spec-breaking` job is red on the PR that
   lands this change. The change is deliberate.

## Consequences

- Callers keep working without a key. A caller that sends one gets replay and reuse protection within one process
  lifetime.
- A reader of `GET /robots` must follow `nextCursor`: `curl .../robots | jq '.items'`.
- If the emulator ever gets a database or more than one replica, the store moves there, and the key may become
  required in a new ADR. Until then the key stays optional.
- `docs/docs/api-reference/rest/**` is regenerated from the spec (`npm run gen-api-docs equipment-emulator`).
