#!/usr/bin/env python3
"""Static validation for the Go backend DevOps assets.

Runs without a Docker daemon (the CI "deploy-config" job and
`make check-deploy`):

* parses every YAML asset (compose, Grafana/Prometheus provisioning, CI);
* checks the Dockerfile (multi-stage, pinned Go, distroless, non-root,
  healthcheck);
* validates the Grafana dashboard JSON and cross-checks every
  ``activity_descriptor_*`` metric it queries against internal/metrics;
* validates n8n workflow exports (structure, connections, retry/safety flags)
  and that the env vars they use are documented and wired in compose;
* checks the static web placeholder (HTML parses, no external resources);
* verifies required deliverables exist;
* checks relative Markdown links in the docs owned by this package;
* scans owned text assets for obvious secret leaks.

Stdlib + PyYAML only (PyYAML ships on GitHub runners). Exit code 1 on failure.
"""

from __future__ import annotations

import html.parser
import json
import os
import re
import shutil
import subprocess
import sys

try:
    import yaml
except ImportError:  # pragma: no cover - helpful message on bare machines
    print("PyYAML is required: pip install pyyaml", file=sys.stderr)
    sys.exit(2)

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

YAML_FILES = [
    "docker-compose.yml",
    ".golangci.yml",
    "deploy/prometheus/prometheus.yml",
    "deploy/grafana/provisioning/datasources/prometheus.yml",
    "deploy/grafana/provisioning/dashboards/dashboards.yml",
    ".github/workflows/ci.yml",
    ".github/workflows/backend-acceptance.yml",
]

REQUIRED_FILES = [
    "Dockerfile",
    ".dockerignore",
    "docker-compose.yml",
    ".env.example",
    ".golangci.yml",
    "Makefile",
    "cmd/api/main.go",
    "internal/metrics/metrics.go",
    "internal/metrics/metrics_test.go",
    "web/index.html",
    "deploy/nginx/default.conf",
    "deploy/prometheus/prometheus.yml",
    "deploy/grafana/provisioning/datasources/prometheus.yml",
    "deploy/grafana/provisioning/dashboards/dashboards.yml",
    "deploy/grafana/dashboards/activity-descriptor.json",
    "deploy/n8n/workflows/README.md",
    "deploy/n8n/workflows/nightly_ingestion.json",
    "docs/DOCKER.md",
    "docs/GRAFANA.md",
    "docs/METRICS.md",
    "docs/N8N.md",
    "docs/CI.md",
    "scripts/check_deploy.py",
]

# Docs owned by the DevOps package: their relative links are validated.
OWNED_DOCS = [
    "docs/DOCKER.md",
    "docs/GRAFANA.md",
    "docs/METRICS.md",
    "docs/N8N.md",
    "docs/CI.md",
    "deploy/n8n/workflows/README.md",
]

SECRET_PATTERNS = [
    re.compile(r"sk-[A-Za-z0-9]{20,}"),
    re.compile(r"BEGIN (?:RSA|OPENSSH|EC) PRIVATE KEY"),
    re.compile(r"AKIA[0-9A-Z]{16}"),
    re.compile(r"ghp_[A-Za-z0-9]{30,}"),
]

# Files scanned for leaked secrets (everything this package adds/modifies).
SECRET_SCAN_GLOBS = [
    "Dockerfile",
    ".dockerignore",
    "docker-compose.yml",
    ".env.example",
    ".golangci.yml",
    "Makefile",
    "cmd/api",
    "internal/metrics",
    "deploy",
    "web",
    "docs/DOCKER.md",
    "docs/GRAFANA.md",
    "docs/METRICS.md",
    "docs/N8N.md",
    "docs/CI.md",
    "scripts/check_deploy.py",
    ".github/workflows/ci.yml",
]

EXTERNAL_RESOURCE_TAGS = {"script", "link", "img", "iframe", "source"}


class HTMLCollector(html.parser.HTMLParser):
    """Collects ids and external resource references from one HTML file."""

    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.ids: list[str] = []
        self.resource_refs: list[tuple[str, str, str]] = []

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        attributes = dict(attrs)
        if attributes.get("id"):
            self.ids.append(attributes["id"])  # type: ignore[arg-type]
        if tag in EXTERNAL_RESOURCE_TAGS:
            for attr in ("src", "href"):
                value = attributes.get(attr)
                if value:
                    self.resource_refs.append((tag, attr, value))


def read(rel: str) -> str:
    return open(os.path.join(ROOT, rel), encoding="utf-8").read()


def fail(message: str) -> None:
    print(f"FAIL: {message}", file=sys.stderr)


def walk_owned() -> list[str]:
    paths: list[str] = []
    for entry in SECRET_SCAN_GLOBS:
        full = os.path.join(ROOT, entry)
        if os.path.isfile(full):
            paths.append(full)
            continue
        for root, dirs, files in os.walk(full):
            dirs[:] = [d for d in dirs if d not in {".git", "bin", "node_modules"}]
            paths.extend(os.path.join(root, name) for name in files)
    return paths


def check_yaml() -> None:
    for rel in YAML_FILES:
        path = os.path.join(ROOT, rel)
        if not os.path.isfile(path):
            raise SystemExit(f"FAIL: missing YAML asset {rel}")
        yaml.safe_load(open(path, encoding="utf-8"))
    print(f"yaml ok: {len(YAML_FILES)} files")


def check_dockerfile() -> None:
    dockerfile = read("Dockerfile")
    stages = [line for line in dockerfile.splitlines() if line.strip().upper().startswith("FROM ")]
    if len(stages) < 2:
        raise SystemExit("FAIL: Dockerfile must be multi-stage (build + runtime)")
    if "golang:1.26.10" not in dockerfile:
        raise SystemExit("FAIL: Dockerfile must pin the Go builder to 1.26.10")
    if "distroless" not in dockerfile:
        raise SystemExit("FAIL: Dockerfile runtime stage should use a distroless base")
    if not re.search(r"(?mi)^USER\s+nonroot", dockerfile):
        raise SystemExit("FAIL: Dockerfile must run as the non-root user")
    if "HEALTHCHECK" not in dockerfile:
        raise SystemExit("FAIL: Dockerfile must define HEALTHCHECK")
    if "CGO_ENABLED=0" not in dockerfile:
        raise SystemExit("FAIL: Dockerfile should build a static binary (CGO_ENABLED=0)")
    if "COPY prompts" not in dockerfile and "prompts" not in dockerfile:
        raise SystemExit("FAIL: Dockerfile must copy the embedded prompts package")
    print("dockerfile ok: multi-stage, pinned Go 1.26.10, distroless, non-root, healthcheck")


def check_compose() -> None:
    compose = yaml.safe_load(read("docker-compose.yml"))
    services = compose.get("services") or {}
    required = {"api", "web", "postgres", "prometheus", "grafana", "n8n", "n8n-postgres"}
    missing = sorted(required - set(services))
    if missing:
        raise SystemExit(f"FAIL: docker-compose.yml is missing services: {missing}")
    api = services["api"]
    if "build" not in api:
        raise SystemExit("FAIL: api service must build from the local Dockerfile")
    healthcheck = api.get("healthcheck", {}).get("test") or []
    if "-healthcheck" not in " ".join(healthcheck):
        raise SystemExit("FAIL: api healthcheck must use the container self-probe (-healthcheck)")
    if "JWT_SECRET" not in (api.get("environment") or {}):
        raise SystemExit("FAIL: api service must receive JWT_SECRET from the environment")
    postgres = services["postgres"]
    if "pgvector" not in str(postgres.get("image", "")):
        raise SystemExit("FAIL: postgres image must be pgvector (backend migrations need the vector extension)")
    profiles = set()
    for service in services.values():
        profiles.update(service.get("profiles") or [])
    for wanted in ("monitoring", "automation"):
        if wanted not in profiles:
            raise SystemExit(f"FAIL: compose profiles must include {wanted!r}")

    docker = shutil.which("docker")
    if docker:
        for args in (
            ["compose", "-f", "docker-compose.yml", "config", "--quiet"],
            ["compose", "-f", "docker-compose.yml", "--profile", "monitoring", "--profile", "automation", "config", "--quiet"],
        ):
            result = subprocess.run([docker, *args], cwd=ROOT, capture_output=True, text=True, check=False)
            if result.returncode != 0:
                raise SystemExit(f"FAIL: docker {' '.join(args)}:\n{result.stderr}")
        print("compose ok (docker compose config --quiet passed for default + profiles)")
    else:
        print("compose ok (docker CLI not found; daemon-level config check skipped)")


def check_dashboard() -> None:
    dashboard = json.loads(read("deploy/grafana/dashboards/activity-descriptor.json"))
    if dashboard.get("uid") != "activity-descriptor":
        raise SystemExit("FAIL: dashboard uid must be activity-descriptor")
    panels = dashboard.get("panels") or []
    if len(panels) < 8:
        raise SystemExit(f"FAIL: dashboard has {len(panels)} panels, expected >= 8")
    titles = " ".join(panel.get("title", "") for panel in panels).lower()
    for required in ("latency", "error", "route", "in-flight"):
        if required not in titles:
            raise SystemExit(f"FAIL: dashboard is missing a {required!r} panel")

    metrics_source = read("internal/metrics/metrics.go")
    documented = set(re.findall(r'newVec\("([a-z_]+)"', metrics_source))
    known = {f"activity_descriptor_{name}" for name in documented}
    referenced: set[str] = set()
    for panel in panels:
        targets = panel.get("targets") or []
        if not targets:
            raise SystemExit(f"FAIL: dashboard panel {panel.get('title')!r} has no queries")
        for target in targets:
            expr = target.get("expr", "")
            if not expr:
                raise SystemExit(f"FAIL: panel {panel.get('title')!r} has an empty query")
            if "run_id" in expr:
                raise SystemExit(f"FAIL: panel {panel.get('title')!r} queries a run identifier label")
            referenced.update(re.findall(r"activity_descriptor_[a-z_]+", expr))
    # Histogram exposition adds _bucket/_sum/_count suffixes to the family name.
    families = {re.sub(r"_(?:bucket|sum|count)$", "", name) for name in referenced}
    unknown = sorted(families - known)
    if unknown:
        raise SystemExit(f"FAIL: dashboard queries undefined metrics: {unknown}")
    for important in (
        "activity_descriptor_http_requests_total",
        "activity_descriptor_http_request_duration_seconds",
        "activity_descriptor_http_in_flight_requests",
    ):
        if important not in families:
            raise SystemExit(f"FAIL: dashboard must query {important}")
    print(f"dashboard ok: {len(panels)} panels, {len(referenced)} metric families referenced")


def check_web() -> None:
    collector = HTMLCollector()
    collector.feed(read("web/index.html"))
    for tag, attr, value in collector.resource_refs:
        if value.startswith(("http://", "https://", "//")):
            raise SystemExit(f"FAIL: external resource in web/index.html: <{tag} {attr}={value!r}>")
    if "/api/v1/health" not in read("web/index.html"):
        raise SystemExit("FAIL: web/index.html must probe /api/v1/health")
    print(f"web ok: html parsed, ids={len(collector.ids)}, no external resources")


def check_workflows() -> None:
    directory = os.path.join(ROOT, "deploy/n8n/workflows")
    workflows: list[tuple[str, dict]] = []
    for name in sorted(os.listdir(directory)):
        if not name.endswith(".json"):
            continue
        rel = f"deploy/n8n/workflows/{name}"
        workflows.append((rel, json.loads(read(rel))))
    if not workflows:
        raise SystemExit("FAIL: no n8n workflow exports found")

    for rel, workflow in workflows:
        for key in ("name", "nodes", "connections", "settings"):
            if key not in workflow:
                raise SystemExit(f"FAIL: {rel} is missing {key!r}")
        nodes = workflow["nodes"]
        if len(nodes) < 3:
            raise SystemExit(f"FAIL: {rel} has too few nodes")
        names: set[str] = set()
        ids: set[str] = set()
        for node in nodes:
            for key in ("id", "name", "type", "typeVersion", "position", "parameters"):
                if key not in node:
                    raise SystemExit(f"FAIL: {rel} node {node.get('name')!r} is missing {key!r}")
            if node["name"] in names or node["id"] in ids:
                raise SystemExit(f"FAIL: {rel} has duplicate node names/ids")
            names.add(node["name"])
            ids.add(node["id"])
            if not (isinstance(node["position"], list) and len(node["position"]) == 2):
                raise SystemExit(f"FAIL: {rel} node {node['name']!r} has an invalid position")
        for source, output in workflow["connections"].items():
            if source not in names:
                raise SystemExit(f"FAIL: {rel} connection source {source!r} is not a node")
            for branch in output.get("main", []):
                for connection in branch:
                    if connection.get("node") not in names:
                        raise SystemExit(f"FAIL: {rel} connection target {connection.get('node')!r} is not a node")
        if workflow.get("active") is not False:
            raise SystemExit(f"FAIL: {rel} must be imported inactive (active=false)")
        for setting in ("saveExecutionProgress", "saveDataErrorExecution", "saveDataSuccessExecution"):
            if setting not in workflow["settings"]:
                raise SystemExit(f"FAIL: {rel} settings are missing execution-history key {setting!r}")

    nightly = dict(workflows)["deploy/n8n/workflows/nightly_ingestion.json"]
    types = {node["type"] for node in nightly["nodes"]}
    required_types = {
        "n8n-nodes-base.scheduleTrigger",
        "n8n-nodes-base.manualTrigger",
        "n8n-nodes-base.httpRequest",
        "n8n-nodes-base.if",
        "n8n-nodes-base.telegram",
        "n8n-nodes-base.set",
    }
    missing_types = sorted(required_types - types)
    if missing_types:
        raise SystemExit(f"FAIL: nightly_ingestion.json is missing node types: {missing_types}")

    ingest = next(node for node in nightly["nodes"] if node["type"] == "n8n-nodes-base.httpRequest")
    if ingest.get("retryOnFail") is not True or int(ingest.get("maxTries", 0)) < 3:
        raise SystemExit("FAIL: ingestion HTTP node must retry (retryOnFail=true, maxTries>=3)")
    if ingest.get("onError") != "continueErrorOutput":
        raise SystemExit("FAIL: ingestion HTTP node must route failures with onError=continueErrorOutput")

    telegram = next(node for node in nightly["nodes"] if node["type"] == "n8n-nodes-base.telegram")
    if not telegram.get("disabled"):
        raise SystemExit("FAIL: Telegram error node must be disabled by default")
    credential_id = telegram.get("credentials", {}).get("telegramApi", {}).get("id", "")
    if not credential_id.startswith("REPLACE_WITH"):
        raise SystemExit("FAIL: Telegram node must carry a placeholder credential id, not a real one")

    env_text = read(".env.example")
    compose_text = read("docker-compose.yml")
    used_env = set(re.findall(r"\$env\.([A-Z0-9_]+)", json.dumps(nightly)))
    for variable in sorted(used_env):
        if variable not in env_text:
            raise SystemExit(f"FAIL: {variable} used by the n8n workflow is missing from .env.example")
        if variable not in compose_text:
            raise SystemExit(f"FAIL: {variable} used by the n8n workflow is not wired in docker-compose.yml")
    print(f"n8n workflows ok: {len(workflows)} file(s), env={sorted(used_env)}")


def check_required_files() -> None:
    missing = [rel for rel in REQUIRED_FILES if not os.path.isfile(os.path.join(ROOT, rel))]
    if missing:
        raise SystemExit(f"FAIL: missing deliverables: {missing}")
    print(f"required files ok: {len(REQUIRED_FILES)} files")


def check_markdown_links() -> None:
    broken: list[str] = []
    for rel in OWNED_DOCS:
        path = os.path.join(ROOT, rel)
        text = read(rel)
        for match in re.finditer(r"\]\(([^)\s]+)\)", text):
            target = match.group(1).split("#", 1)[0].strip()
            if not target or target.startswith(("http://", "https://", "mailto:")):
                continue
            resolved = os.path.normpath(os.path.join(os.path.dirname(path), target))
            if not os.path.exists(resolved):
                broken.append(f"{rel} -> {target}")
    if broken:
        raise SystemExit("FAIL: broken markdown links:\n  " + "\n  ".join(broken))
    print("markdown links ok")


def check_secrets() -> None:
    hits: list[str] = []
    for path in walk_owned():
        rel = os.path.relpath(path, ROOT)
        try:
            text = open(path, encoding="utf-8", errors="ignore").read()
        except OSError:
            continue
        for pattern in SECRET_PATTERNS:
            if pattern.search(text):
                hits.append(f"{rel}: {pattern.pattern}")
    if hits:
        raise SystemExit("FAIL: possible secret material committed:\n  " + "\n  ".join(hits))
    print("secret scan ok")


def main() -> int:
    check_yaml()
    check_dockerfile()
    check_compose()
    check_dashboard()
    check_web()
    check_workflows()
    check_required_files()
    check_markdown_links()
    check_secrets()
    print("deploy config OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
