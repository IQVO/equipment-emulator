#!/usr/bin/env bash
# Chart render assertions. Run: bash charts/equipment-emulator/tests/render_test.sh (needs helm)
set -euo pipefail
chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }
render() { helm template equipment-emulator "$chart_dir" "$@"; }

deployment="$(render --show-only templates/deployment.yaml)"
configmap="$(render --show-only templates/configmap.yaml)"
cm_name="$(awk '/^  name:/ {print $2; exit}' <<<"$configmap")"
[[ -n "$cm_name" ]] || fail "could not read the ConfigMap name"

grep -A3 'envFrom:' <<<"$deployment" | grep -q "name: $cm_name" || fail "Deployment does not mount the ConfigMap via envFrom"
for v in MQTT_URL HTTP_ADDR WORLD_FILE SIM_SPEED TICK_INTERVAL; do
  grep -q "name: $v\$" <<<"$deployment" || fail "Deployment does not render $v"
  grep -q "^  $v: " <<<"$configmap" || fail "ConfigMap does not carry $v"
done
grep -q 'tcp://mosquitto.warehouse-systems.svc.cluster.local:1883' <<<"$deployment" || fail "default MQTT_URL missing"
grep -q '^  world.yaml: ' <<<"$configmap" || fail "ConfigMap does not carry world.yaml"
grep -q 'path: /healthz' <<<"$deployment" || fail "liveness probe must hit /healthz"
grep -q 'path: /readyz' <<<"$deployment" || fail "readiness probe must hit /readyz"
grep -q 'timeoutSeconds: 3' <<<"$deployment" || fail "readiness timeoutSeconds must be 3"
grep -q 'cpu: 25m' <<<"$deployment" || fail "cpu request must be 25m"
grep -q 'memory: 64Mi' <<<"$deployment" || fail "memory request must be 64Mi"
[[ "$(render | grep -c '^kind: Deployment')" == 1 ]] || fail "expected exactly one Deployment"
renamed="$(render --show-only templates/deployment.yaml --set mqtt.url=tcp://x:1 --set config.simSpeed=5)"
grep -A1 'name: MQTT_URL$' <<<"$renamed" | grep -q 'tcp://x:1' || fail "mqtt.url does not reach MQTT_URL"
grep -A1 'name: SIM_SPEED$' <<<"$renamed" | grep -q '"5"' || fail "config.simSpeed does not reach SIM_SPEED"

# ci/all-components-values.yaml sets every optional value: each must reach the render.
all="$(render -f "$chart_dir/ci/all-components-values.yaml")"
for want in 'name: ghcr-pull' 'serviceAccountName: equipment-emulator-ci' 'example.invalid/role: emulator' \
  'example.invalid/scrape: "false"' 'tier: wcs-test' 'name: EXTRA_FLAG' 'kubernetes.io/os: linux' \
  'key: dedicated' 'preferredDuringSchedulingIgnoredDuringExecution' 'tcp://mosquitto.example.invalid:1883' \
  'image: "\?ghcr.io/iqvo/equipment-emulator:ci'; do
  grep -q -- "$want" <<<"$all" || fail "all-components-values: '$want' does not reach the render"
done
[[ "$(grep -c '^kind: Deployment' <<<"$all")" == 1 ]] || fail "all-components-values: expected exactly one Deployment"

# Observability env (ADR 0005): rendered by default, OTEL_* only while otel.enabled.
env_value() { grep -A1 "name: $1\$" <<<"$2" | awk '/value:/ {print $2; exit}'; }
[[ "$(env_value OTEL_EXPORTER_OTLP_ENDPOINT "$deployment")" == '"otel-collector.observability.svc.cluster.local:4317"' ]] || fail "default OTEL_EXPORTER_OTLP_ENDPOINT is not the cluster Collector"
[[ "$(env_value OTEL_SERVICE_NAME "$deployment")" == '"equipment-emulator"' ]] || fail "default OTEL_SERVICE_NAME must be equipment-emulator"
[[ "$(env_value SERVICE_VERSION "$deployment")" == '"local"' ]] || fail "SERVICE_VERSION must fall back to image.tag"
[[ "$(env_value ENVIRONMENT "$deployment")" == '"production"' ]] || fail "default ENVIRONMENT must be production"
[[ "$(env_value SERVICE_VERSION "$all")" == '"v9.9.9-ci"' ]] || fail "serviceVersion does not reach SERVICE_VERSION"
[[ "$(env_value OTEL_SERVICE_NAME "$all")" == '"equipment-emulator-ci"' ]] || fail "otel.serviceName does not reach OTEL_SERVICE_NAME"
[[ "$(env_value ENVIRONMENT "$all")" == '"ci"' ]] || fail "environment does not reach ENVIRONMENT"
off="$(render --show-only templates/deployment.yaml --set otel.enabled=false)"
! grep -q 'name: OTEL_' <<<"$off" || fail "otel.enabled=false must render no OTEL_* variable"
grep -q 'name: SERVICE_VERSION$' <<<"$off" || fail "SERVICE_VERSION must render with otel off too"
for owned in OTEL_EXPORTER_OTLP_ENDPOINT OTEL_SERVICE_NAME SERVICE_VERSION ENVIRONMENT; do
  if render --set "extraEnv[0].name=$owned" --set "extraEnv[0].value=x" >/dev/null 2>&1; then
    fail "extraEnv must refuse $owned (the chart renders it)"
  fi
done

# Every env var the binary reads must be rendered by the Deployment (helm env
# contract). The scan covers the config package, the composition root and the
# telemetry adapter; the floor catches a scan looking in the wrong place.
repo_root="$(cd "$chart_dir/../.." && pwd)"
binary_env="$(cat "$repo_root"/internal/config/*.go "$repo_root"/cmd/emulator/*.go "$repo_root"/internal/adapters/outbound/telemetry/*.go \
  | grep -v '^\s*//' | grep -oE '(Getenv|getenv|env)\("[A-Z][A-Z0-9_]+"' | grep -oE '"[A-Z][A-Z0-9_]+"' | tr -d '"' | sort -u)"
found="$(wc -l <<<"$binary_env" | tr -d ' ')"
(( found >= 8 )) || fail "found only $found env names the binary reads: the scan is looking in the wrong place"
for v in $binary_env; do
  grep -q "name: $v\$" <<<"$all" || fail "the Deployment does not render $v, which cmd/emulator reads"
done
echo "render_test: ok"
