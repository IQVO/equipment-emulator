#!/usr/bin/env python3
"""Assert the Service selects exactly one Deployment (the emulator component).

`app.kubernetes.io/name` + `instance` are identical on every pod of a release,
so a Service must also pin `app.kubernetes.io/component`.

Run: python3 charts/equipment-emulator/tests/test_service_selectors.py
Needs: helm, PyYAML.
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

CHART_DIR = Path(__file__).resolve().parents[1]


def render() -> list[dict]:
    result = subprocess.run(
        ["helm", "template", "equipment-emulator", str(CHART_DIR)],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        raise SystemExit(f"FAIL: helm template:\n{result.stderr}")
    try:
        import yaml  # type: ignore
    except ModuleNotFoundError:
        print("SKIP: PyYAML not available; cannot assert selectors", file=sys.stderr)
        raise SystemExit(0)
    return [d for d in yaml.safe_load_all(result.stdout) if d]


def matches(selector: dict, labels: dict) -> bool:
    return bool(selector) and all(labels.get(k) == v for k, v in selector.items())


def main() -> None:
    docs = render()
    deployments = [d for d in docs if d["kind"] == "Deployment"]
    services = [d for d in docs if d["kind"] == "Service"]
    failures: list[str] = []
    if len(deployments) != 1:
        failures.append(f"want exactly 1 Deployment, got {len(deployments)}")
    if len(services) != 1:
        failures.append(f"want exactly 1 Service, got {len(services)}")
    for svc in services:
        sel = svc["spec"]["selector"]
        if sel.get("app.kubernetes.io/component") != "emulator":
            failures.append("the Service must pin component=emulator")
        hit = [d["metadata"]["name"] for d in deployments if matches(sel, d["spec"]["template"]["metadata"]["labels"])]
        if len(hit) != 1:
            failures.append(f"Service {svc['metadata']['name']} selects {len(hit)} Deployments: {hit}")
    for dep in deployments:
        if dep["spec"]["selector"]["matchLabels"].get("app.kubernetes.io/component") != "emulator":
            failures.append("the Deployment selector must pin component=emulator")
    if failures:
        raise SystemExit("FAIL:\n  " + "\n  ".join(failures))
    print("ok: the Service selects exactly the emulator Deployment")


if __name__ == "__main__":
    main()
