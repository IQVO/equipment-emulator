# equipment-emulator

The equipment emulator is the stand-in for ISA-95 levels 1 and 0 of the WCS
tier: simulated AMR robots that speak VDA 5050 v3 and a simulated AS/RS PLC
that speaks a telegram protocol, both over MQTT. It lets the WCS services
(fleet control, `asrs-control`) be developed and tested without hardware. It
owns no business data and calls no sibling service: it only answers MQTT
traffic and offers a small admin API to inspect the simulated world, inject
faults and seed stock. It is test infrastructure, not a deployable product
context.

## Disclaimer

This project is a domain-driven design study. It is not associated with,
endorsed by or affiliated with any company, and it uses no company's code,
data, logos or trademarks. All names, figures and examples are illustrative
or synthetic.

## Documentation

| Document | What it covers |
| --- | --- |
| [apis/asyncapi.yaml](apis/asyncapi.yaml) | AsyncAPI 2.6 contract of every MQTT channel and message (VDA 5050 and telegram) |
| [docs/vda5050-subset.md](docs/vda5050-subset.md) | Which VDA 5050 v3.0.0 features the robots implement and which they do not |
| [docs/telegram-protocol.md](docs/telegram-protocol.md) | AS/RS telegram protocol v1: transport, fields, handshake, job outcomes |
| [apis/openapi.yaml](apis/openapi.yaml) | The admin REST API (inspection, fault injection, seeding, health probes) |
| [HARNESS.md](HARNESS.md) | The quality harness: every sensor's purpose, cost and lifecycle position |

## Running it locally

Start a broker, then the emulator:

```bash
docker run -p 1883:1883 eclipse-mosquitto:2.0.20 mosquitto -c /mosquitto-no-auth.conf
WORLD_FILE=config/world.example.yaml SIM_SPEED=10 go run ./cmd/emulator
```

| Variable | Default | Meaning |
| --- | --- | --- |
| `WORLD_FILE` | required | YAML file describing the robots and the AS/RS (see `config/world.example.yaml`) |
| `MQTT_URL` | `tcp://localhost:1883` | MQTT broker |
| `HTTP_ADDR` | `:8080` | Admin API listen address |
| `SIM_SPEED` | `1` | Simulation speed multiplier |
| `TICK_INTERVAL` | `100ms` | Wall-clock interval between simulation ticks |

## API

The admin API (`/healthz`, `/readyz`, `/robots`, `/asrs/{asrsId}`, `/faults`,
seeding routes) is specified in [apis/openapi.yaml](apis/openapi.yaml); this
README does not repeat it. It has no authentication layer.

The MQTT protocols are specified in [apis/asyncapi.yaml](apis/asyncapi.yaml):

- `vda5050/v3/<manufacturer>/<serialNumber>/{order,instantActions,state,connection,factsheet}`
  (see [docs/vda5050-subset.md](docs/vda5050-subset.md))
- `asrs/v1/<asrsId>/{down,up}` (see [docs/telegram-protocol.md](docs/telegram-protocol.md))

## Local development / quality gate

`make check` runs format, vet, build, lint and the tests; `make check-all`
adds coverage, the architecture fitness tests and the BDD features.
`make guide-lint` lints the agent guides. Lint the AsyncAPI contract with:

```bash
npx @stoplight/spectral-cli@6 lint apis/asyncapi.yaml --ruleset .spectral.asyncapi.yaml --fail-severity=warn
```

## License

MIT, see [LICENSE](LICENSE).
