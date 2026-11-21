# Before the first release: equipment-emulator

The first merge to `main` runs `docker-publish` and `release`. Two services scaffolded from
warehouse-harness-template (pick-face-replenishment, task-interleaving) each needed a rework because
packaging was missing or wrong when that moment came. Work through this list on a feature branch,
**before** the first `develop` → `main` PR (there is no `main` yet). The emulator has no database,
no Kafka and no consumer groups, so the template's rows for `DATABASE_URL` Secrets and consumer
group ids are dropped here on purpose.

| # | Item | State on develop | Guarded by |
|---|------|------------------|------------|
| 1 | ✅ **Real runtime only.** `cmd/emulator` wires the real MQTT gateways; no in-memory adapter, no `*_MODE` fallback. It exits at boot without `WORLD_FILE` or with an invalid world. | Done. | `TestRuntimeBinaryHasNoInMemoryAdapters`; `internal/config` tests. |
| 2 | ✅ **No Go file at the repo root.** The godog runner lives in `features/`, the binary in `cmd/emulator/`. | Done. | `TestNoGoFilesAtRepoRoot`. |
| 3 | ✅ **`Dockerfile` builds every `cmd/*`**; `ENTRYPOINT ["./emulator"]`, `EXPOSE 8080` matches `HTTP_ADDR`. | Done. Verified 2026-11-21 on develop `342ff09`: `docker build --platform linux/amd64 .` succeeds. The image ships `config/world.example.yaml` as `WORLD_FILE=/etc/emulator/world.yaml`, so `docker run --rm <image>` boots the world and exits 1 on the MQTT connect (no broker at the default `tcp://localhost:1883`) after the 15 s connect wait, not on a missing `WORLD_FILE`. | `trivy-scan` on PRs; `docker-publish` on push to `main`. |
| 4 | ✅ **`.dockerignore`** excludes docs, charts, harness and test output. | Done. | none: review once. |
| 5 | ✅ **Helm chart `charts/equipment-emulator/`**, `Chart.yaml` `name: equipment-emulator`, matching the `release` job's `helm package` file name. | Done. | `helm-lint` (PRs) and the `release` job. |
| 6 | **Pod hardening and shutdown room.** `runAsNonRoot`, `readOnlyRootFilesystem: true`, `allowPrivilegeEscalation: false`, `capabilities.drop: [ALL]` are in `values.yaml`. The HTTP shutdown is bounded at 5 s, inside the Kubernetes default 30 s grace period. | Hardening done. Not ticked: the grace period is not set explicitly and not chart-tested (the 5 s HTTP bound sits inside the Kubernetes default 30 s). | review. |
| 7 | ✅ **Chart tests** `charts/equipment-emulator/tests/` (`render_test.sh`, `test_service_selectors.py`): envFrom ConfigMap, every env var `cmd/emulator` reads, `world.yaml` in the ConfigMap, probes on `/healthz` and `/readyz`, one Deployment, every Service selects exactly one Deployment. | Done. | `chart-tests` job. |
| 8 | ✅ **Optional CI jobs match the service.** No `web/`: no `web` job and no knip step in `drift`. The Docusaurus site under `docs/` has the `Docs` workflow (`docs.yml`, builds on PRs, deploys Pages from `main`) and the `docs-api-drift` job. `api-lint` lints both `apis/openapi.yaml` and `apis/asyncapi.yaml`. GitHub Pages is enabled with source "GitHub Actions"; the first push to `main` publishes the site. | Done. | first PR's CI run; the first `Docs` run on `main`. |
| 9 | ✅ **Registry access**: images push to `ghcr.io/iqvo` with the Actions token (`packages: write` on `docker-publish`/`release`). | Done: `ci.yml` grants `packages: write` and logs in with `GITHUB_TOKEN`; the identical job shape published `ghcr.io/iqvo/asrs-control:v0.1.0` on 2026-11-21. No Docker Hub secrets needed. | none. |
| 10 | ✅ **`govulncheck` is clean** (`make vuln`, CI `vuln` job). | Done: `make vuln` prints "No vulnerabilities found." on develop `342ff09` (2026-11-21); CI `vuln` green. | `vuln` job (a `docker-publish` prerequisite). |
| 11 | **Deployment target.** The emulator needs the WCS Mosquitto broker (`mqtt.url`, default `tcp://mosquitto.warehouse-systems.svc.cluster.local:1883`) and an entry in `warehouse-infra`'s application registry. | Not ticked: not yet registered in `warehouse-infra` (a later wave). | none. |

Tick this list in the release PR and keep the file: it is the emulator's packaging record.
