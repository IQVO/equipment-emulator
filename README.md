# equipment-emulator

The equipment emulator is the stand-in for ISA-95 levels 1 and 0 of the WCS
tier: simulated AMR robots that speak VDA 5050 v3 and a simulated AS/RS PLC
that speaks a telegram protocol, both over MQTT. It lets the WCS services
(fleet control, `asrs-control`) be developed and tested without hardware. It
owns no business data and calls no sibling service: it only answers MQTT
traffic and offers a small admin API to inspect the simulated world, inject
faults and seed stock. It is test infrastructure, not a bounded context and
not a deployable product context ([ADR 0001](docs/adr/0001-emulator-role-and-boundaries.md)).

## Disclaimer

This project is a domain-driven design study. It is not associated with,
endorsed by or affiliated with any company, and it uses no company's code,
data, logos or trademarks. All names, figures and examples are illustrative
or synthetic.

## Documentation

The Docusaurus site under [`docs/`](docs/) publishes to
<https://iqvo.github.io/equipment-emulator/> from `main` (the `Docs` workflow
builds it on every pull request; there is no `main` release yet, so the site is
not live until the first release).

| Page | What it covers |
| --- | --- |
| [Introduction](docs/docs/overview/introduction.md) | What the emulator simulates and which fleet surfaces do not apply |
| [Architecture](docs/docs/overview/architecture.md) | The binary, its layers, the simulation loop and simulated time |
| [Quickstart](docs/docs/overview/quickstart.md) | Run it against a local Mosquitto, seed stock, inject a fault |
| [Configuration](docs/docs/operations/configuration.md), [Runbook](docs/docs/operations/runbook.md), [Observability](docs/docs/operations/observability.md), [Troubleshooting](docs/docs/operations/troubleshooting.md) | Operating it |
| [Testing](docs/docs/development/testing.md) | Suites, local gates, CI jobs |
| [API overview](docs/docs/api-reference/overview.md), [MQTT protocols](docs/docs/api-reference/mqtt.md) | The admin REST API (reference generated from `apis/openapi.yaml`) and the MQTT channels |
| [docs/vda5050-subset.md](docs/vda5050-subset.md) | Which VDA 5050 v3.0.0 features the robots implement and which they do not |
| [docs/telegram-protocol.md](docs/telegram-protocol.md) | AS/RS telegram protocol v1: transport, fields, handshake, job outcomes |
| [docs/adr/](docs/adr/index.md) | Architecture decision records |
| [HARNESS.md](HARNESS.md) | The quality harness: every sensor's purpose, cost and lifecycle position |

## Architecture

Hexagonal, one binary (`cmd/emulator`), all state in memory:

```text
cmd/emulator/                  wiring: config, gateways, engine, HTTP server, tick loop
internal/domain/{robot,asrs,simclock}
internal/application/engine/   the simulated world; routes robot pick/drop to AS/RS ports; faults
internal/adapters/vda5050/     VDA 5050 v3 wire messages and mapping
internal/adapters/telegram/    telegram codec, ACK deduplication, retransmitter
internal/adapters/mqtt/        one MQTT client per robot, one per AS/RS
internal/adapters/inbound/http admin REST API
internal/config/               environment and world file
```

The layering is enforced by the fitness tests in `internal/architecture/`
(`make arch-test`).

## Integration

The emulator calls no service. It publishes no Kafka event and no CloudEvents
type, has no database, no MCP server, no A2A agent and no Kong route: none of
these apply to test infrastructure ([ADR 0001](docs/adr/0001-emulator-role-and-boundaries.md)).
Its peers connect to the same MQTT broker:

| Peer | Protocol | Topics |
| --- | --- | --- |
| `amr-fleet-control` | VDA 5050 v3, robot side ([ADR 0002](docs/adr/0002-vda5050-v3-robot-side-subset.md)) | `vda5050/v3/<manufacturer>/<serialNumber>/{order,instantActions,state,connection,factsheet}` |
| `asrs-control` | AS/RS telegram protocol v1 ([ADR 0003](docs/adr/0003-asrs-telegram-protocol-v1.md); asrs-control conforms, its ADR 0002) | `asrs/v1/<asrsId>/{down,up}` |

## Running it

Start a broker, then the emulator:

```bash
docker run -p 1883:1883 eclipse-mosquitto:2.0.20 mosquitto -c /mosquitto-no-auth.conf
WORLD_FILE=config/world.example.yaml SIM_SPEED=10 go run ./cmd/emulator
```

### Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `WORLD_FILE` | required | YAML file describing the robots and the AS/RS (see `config/world.example.yaml`) |
| `MQTT_URL` | `tcp://localhost:1883` | MQTT broker |
| `HTTP_ADDR` | `:8080` | Admin API listen address |
| `SIM_SPEED` | `1` | Simulated seconds per wall-clock second (must be positive) |
| `TICK_INTERVAL` | `100ms` | Wall-clock interval between simulation ticks |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty | OTLP/gRPC Collector endpoint; empty turns telemetry off |
| `OTEL_SERVICE_NAME` | `equipment-emulator` | `service.name` |
| `SERVICE_VERSION` | `dev` | `service.version` |
| `ENVIRONMENT` | `local` | `deployment.environment.name` |

## Observability

The fleet OpenTelemetry standard ([ADR 0005](docs/adr/0005-opentelemetry-traces-and-metrics.md)):
JSON logs on stdout carrying `trace_id`/`span_id`, and traces and metrics over OTLP/gRPC
once `OTEL_EXPORTER_OTLP_ENDPOINT` is set (the chart sets it). Spans cover every admin
request, every VDA 5050 message and every telegram (robot serial, telegram id and type);
metrics count orders by outcome, telegrams by direction and type, retransmits and
expiries, robots by state, and the simulated-time ratio. Details:
[docs/docs/operations/observability.md](docs/docs/operations/observability.md).

## API

The admin API (`/healthz`, `/readyz`, `/robots`, `/asrs/{asrsId}`, `/faults`,
seeding routes) is specified in [apis/openapi.yaml](apis/openapi.yaml); this
README does not repeat it. Errors are `application/problem+json` with a stable
`code`; fault injection is idempotent (`active` switches a fault on or off). Every
POST takes an optional `Idempotency-Key` (replayed for 24 hours from process memory,
lost on restart), and `GET /robots` answers a keyset page `{items, nextCursor}`
([ADR 0006](docs/adr/0006-rest-guideline-on-the-admin-api.md)). It has no
authentication layer ([ADR 0004](docs/adr/0004-simulated-time-fault-injection-and-admin-api.md)).

The MQTT protocols are specified in [apis/asyncapi.yaml](apis/asyncapi.yaml):

- `vda5050/v3/<manufacturer>/<serialNumber>/{order,instantActions,state,connection,factsheet}`
  (see [docs/vda5050-subset.md](docs/vda5050-subset.md))
- `asrs/v1/<asrsId>/{down,up}` (see [docs/telegram-protocol.md](docs/telegram-protocol.md))

## Helm chart

`charts/equipment-emulator/` renders one Deployment, one Service, one
ServiceAccount and one ConfigMap that carries the environment (mounted with
`envFrom`) and the `world:` value as `world.yaml`. `mqtt.url` defaults to the WCS
Mosquitto broker. `ci/default-values.yaml` and `ci/all-components-values.yaml`
(every optional value) are linted by `helm-lint`; the tests in
`charts/equipment-emulator/tests/` run in `chart-tests`. Not yet registered in
`warehouse-infra`.

## Local development / quality gate

`make check` runs format, vet, build, lint and the tests; `make check-all`
adds coverage, the architecture fitness tests and the BDD features.
`make guide-lint` lints the agent guides. Lint the AsyncAPI contract with:

```bash
npx @stoplight/spectral-cli@6 lint apis/asyncapi.yaml --ruleset .spectral.asyncapi.yaml --fail-severity=warn
```

The docs site: `cd docs && npm ci && npm run build`; after a change to
`apis/openapi.yaml` run `npm run gen-api-docs equipment-emulator` and commit
`docs/docs/api-reference/rest` (CI `docs-api-drift`).

## Tests

- Unit tests (`make test`); 90% coverage gate on domain and application (`make coverage`).
- BDD: `features/robot.feature`, `features/asrs.feature` (`make bdd`).
- Integration: MQTT gateways against Mosquitto 2.0.20 via testcontainers (`make integration`).
- Mutation: gremlins on `internal/domain/robot` and `internal/domain` (`make mutation`, `make mutation-full`).
- Architecture fitness tests (`make arch-test`); chart tests in `charts/equipment-emulator/tests/`.

CI jobs (`.github/workflows/ci.yml`): `lint`, `guide-lint`, `complexity`,
`file-length`, `test`, `bdd`, `integration`, `mutation-fast`, `mutation`,
`drift`, `api-lint`, `vuln`, `docs-api-drift`, `helm-lint`, `chart-tests`,
`arch-test`, `spec-breaking`, `trivy-scan`, `docker-publish`, `release`
(`mutation` and `drift` run on schedule, `docker-publish` and `release` on `main`).
The `Docs` workflow builds the site.

## Architecture Decision Records

Four ADRs in [docs/adr/](docs/adr/index.md): the emulator's role and boundaries,
the VDA 5050 v3 subset, the AS/RS telegram protocol v1, and simulated time,
fault injection and the admin API.

## License

MIT, see [LICENSE](LICENSE).
