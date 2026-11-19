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
    "deploy/prometheus/prometheus.host.yml",
    "deploy/prometheus/prometheus.server.yml",
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
    "internal/llm/metrics/metrics.go",
    "web/index.html",
    "deploy/nginx/default.conf",
    "deploy/nginx/server.conf",
    "deploy/prometheus/prometheus.yml",
    "deploy/prometheus/prometheus.host.yml",
    "deploy/prometheus/prometheus.server.yml",
    "deploy/grafana/provisioning/datasources/prometheus.yml",
    "deploy/grafana/provisioning/dashboards/dashboards.yml",
    "deploy/grafana/dashboards/activity-descriptor.json",
    "deploy/grafana/dashboards/activity-descriptor-business.json",
    "deploy/n8n/workflows/README.md",
    "deploy/n8n/workflows/nightly_ingestion.json",
    "deploy/n8n/workflows/notion_email_notifications.json",
    "migrations_optional/vector/20261109010000_vector_retrieval.sql",
    "migrations_optional/vector/20261109010001_lexical_fallback.sql",
    "migrations_optional/vector/VECTOR_STORAGE_GUIDE.md",
    "docs/DOCKER.md",
    "docs/DOCKER_SERVER.md",
    "docs/GRAFANA.md",
    "docs/METRICS.md",
    "docs/N8N.md",
    "docs/CI.md",
    "scripts/check_deploy.py",
]

# Docs owned by the DevOps package: their relative links are validated.
OWNED_DOCS = [
    "docs/DOCKER.md",
    "docs/DOCKER_SERVER.md",
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
    "docs/DOCKER_SERVER.md",
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
    # Full backend targets and the default-image contract (CI builds without
    # --target, so the last stage must stay the demo api image).
    stages = re.findall(r"(?mi)^FROM\s+\S+\s+AS\s+(\S+)", dockerfile)
    for stage in ("build", "server-build", "goose-build", "frontend-build", "server", "migrate", "web", "api"):
        if stage not in stages:
            raise SystemExit(f"FAIL: Dockerfile is missing the {stage!r} build stage")
    if stages[-1] != "api":
        raise SystemExit(f"FAIL: last Dockerfile stage must be 'api' (default build target), got {stages[-1]!r}")
    for marker in ("./cmd/server", "goose@v3.29.0", "api-docs", "/app/healthcheck"):
        if marker not in dockerfile:
            raise SystemExit(f"FAIL: Dockerfile must include {marker!r}")
    # Full-stack web target: the SPA is built inside the image and served by
    # nginx with the same-origin proxy config.
    for marker in ("node:22.20", "npm ci", "npm run build", "nginx:1.27-alpine", "deploy/nginx/server.conf"):
        if marker not in dockerfile:
            raise SystemExit(f"FAIL: Dockerfile web target must include {marker!r}")
    print(
        "dockerfile ok: multi-stage, pinned Go 1.26.10/Node 22.20, distroless server/migrate, "
        "nginx web target, non-root, healthchecks"
    )


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
    for wanted in ("monitoring", "automation", "server"):
        if wanted not in profiles:
            raise SystemExit(f"FAIL: compose profiles must include {wanted!r}")

    # Full backend profile (cmd/server): the migrate service must gate the
    # server on a successful goose run, and the host port must not collide with
    # the demo api (8080) or the web placeholder (8081).
    migrate = services.get("migrate")
    if not migrate:
        raise SystemExit("FAIL: docker-compose.yml must define the 'migrate' goose service")
    if (migrate.get("build") or {}).get("target") != "migrate":
        raise SystemExit("FAIL: compose migrate service must build Dockerfile target 'migrate'")
    if "server" not in (migrate.get("profiles") or []):
        raise SystemExit("FAIL: migrate service must be behind the 'server' profile")

    server = services.get("server")
    if not server:
        raise SystemExit("FAIL: docker-compose.yml must define the 'server' full-backend service")
    if (server.get("build") or {}).get("target") != "server":
        raise SystemExit("FAIL: compose server service must build Dockerfile target 'server'")
    if "server" not in (server.get("profiles") or []):
        raise SystemExit("FAIL: server service must be behind the 'server' profile")
    if not (server.get("healthcheck") or {}).get("test"):
        raise SystemExit("FAIL: server service must define a healthcheck")
    server_ports = " ".join(str(port) for port in server.get("ports") or [])
    if "${SERVER_PORT" not in server_ports or ":8080" not in server_ports:
        raise SystemExit("FAIL: server service must map configurable ${SERVER_PORT:-...}:8080")
    server_deps = server.get("depends_on") or {}
    if (server_deps.get("postgres") or {}).get("condition") != "service_healthy":
        raise SystemExit("FAIL: server service must depend on a healthy postgres")
    if (server_deps.get("migrate") or {}).get("condition") != "service_completed_successfully":
        raise SystemExit("FAIL: server must wait for migrate (condition: service_completed_successfully)")
    if "JWT_SECRET" in str(server.get("environment") or {}):
        raise SystemExit("FAIL: server must not hard-code JWT_SECRET; use optional env_file/.env")

    # Optional vector migrations: a second one-shot goose run with its own
    # version table (goose_vector_versions), gated after the main migrations and
    # before the backend, so a fresh DB gets both groups automatically.
    migrate_vector = services.get("migrate-vector")
    if not migrate_vector:
        raise SystemExit("FAIL: docker-compose.yml must define the 'migrate-vector' goose service")
    if (migrate_vector.get("build") or {}).get("target") != "migrate":
        raise SystemExit("FAIL: compose migrate-vector service must build Dockerfile target 'migrate'")
    if "server" not in (migrate_vector.get("profiles") or []):
        raise SystemExit("FAIL: migrate-vector service must be behind the 'server' profile")
    vector_command = " ".join(str(part) for part in migrate_vector.get("command") or [])
    for flag in ("-table goose_vector_versions", "-dir /migrations_optional/vector", "up"):
        if flag not in vector_command:
            raise SystemExit(f"FAIL: migrate-vector command must contain {flag!r}")
    vector_deps = migrate_vector.get("depends_on") or {}
    if (vector_deps.get("postgres") or {}).get("condition") != "service_healthy":
        raise SystemExit("FAIL: migrate-vector must depend on a healthy postgres")
    if (vector_deps.get("migrate") or {}).get("condition") != "service_completed_successfully":
        raise SystemExit("FAIL: migrate-vector must wait for the main migrate run")
    if (server_deps.get("migrate-vector") or {}).get("condition") != "service_completed_successfully":
        raise SystemExit("FAIL: server must wait for migrate-vector (condition: service_completed_successfully)")

    # Full-stack web entry point: built SPA + nginx reverse proxy for the
    # `server` profile, one public port, no second origin/CORS.
    web_server = services.get("web-server")
    if not web_server:
        raise SystemExit("FAIL: docker-compose.yml must define the 'web-server' full-stack UI service")
    if (web_server.get("build") or {}).get("target") != "web":
        raise SystemExit("FAIL: compose web-server service must build Dockerfile target 'web'")
    if "server" not in (web_server.get("profiles") or []):
        raise SystemExit("FAIL: web-server service must be behind the 'server' profile")
    web_ports = " ".join(str(port) for port in web_server.get("ports") or [])
    if "${SERVER_WEB_PORT" not in web_ports or ":80" not in web_ports:
        raise SystemExit("FAIL: web-server must map configurable ${SERVER_WEB_PORT:-...}:80")
    web_deps = web_server.get("depends_on") or {}
    if (web_deps.get("server") or {}).get("condition") != "service_healthy":
        raise SystemExit("FAIL: web-server must wait for a healthy server")
    if not (web_server.get("healthcheck") or {}).get("test"):
        raise SystemExit("FAIL: web-server service must define a healthcheck")

    docker = shutil.which("docker")
    if docker:
        for args in (
            ["compose", "-f", "docker-compose.yml", "config", "--quiet"],
            ["compose", "-f", "docker-compose.yml", "--profile", "monitoring", "--profile", "automation", "config", "--quiet"],
            ["compose", "-f", "docker-compose.yml", "--profile", "server", "config", "--quiet"],
            ["compose", "-f", "docker-compose.yml", "--profile", "server", "--profile", "monitoring", "config", "--quiet"],
        ):
            result = subprocess.run([docker, *args], cwd=ROOT, capture_output=True, text=True, check=False)
            if result.returncode != 0:
                raise SystemExit(f"FAIL: docker {' '.join(args)}:\n{result.stderr}")
        print("compose ok (docker compose config --quiet passed for default + profiles)")
    else:
        print("compose ok (docker CLI not found; daemon-level config check skipped)")


def check_nginx() -> None:
    """Validate the demo placeholder proxy and the full-stack `web` image config."""
    demo = read("deploy/nginx/default.conf")
    for marker in (
        "location /api/",
        "proxy_pass http://api:8080",
        "try_files $uri $uri/ /index.html",
    ):
        if marker not in demo:
            raise SystemExit(f"FAIL: deploy/nginx/default.conf must contain {marker!r}")
    full = read("deploy/nginx/server.conf")
    for marker in (
        "location /api/",
        "resolver 127.0.0.11 ",
        "set $modeus_backend http://server:8080;",
        "proxy_pass $modeus_backend;",
        "location /swagger/",
        "location = /metrics",
        "try_files $uri $uri/ /index.html",
        "proxy_buffering off",
    ):
        if marker not in full:
            raise SystemExit(f"FAIL: deploy/nginx/server.conf must contain {marker!r}")
    if "api:8080" in full or "proxy_pass http://server:" in full:
        raise SystemExit("FAIL: deploy/nginx/server.conf must resolve the full backend through Docker DNS")
    print("nginx ok: demo api proxy + full-stack SPA/API/Swagger/metrics single-origin proxy")


def check_prometheus() -> None:
    """Validate the three scrape modes and their target/job contract."""
    expected_targets = {
        "deploy/prometheus/prometheus.yml": "api:8080",
        "deploy/prometheus/prometheus.host.yml": "host.docker.internal:8080",
        "deploy/prometheus/prometheus.server.yml": "server:8080",
    }
    jobs_by_config: dict[str, list[tuple[str, list[str]]]] = {}
    for rel in expected_targets:
        config = yaml.safe_load(read(rel))
        if not any("alerts.yml" in str(rule) for rule in config.get("rule_files") or []):
            raise SystemExit(f"FAIL: {rel} must load deploy/prometheus/alerts.yml")
        jobs = []
        for job in config.get("scrape_configs") or []:
            targets = [target for cfg in job.get("static_configs") or [] for target in cfg.get("targets") or []]
            if not targets:
                raise SystemExit(f"FAIL: {rel} job {job.get('job_name')!r} has no static targets")
            jobs.append((job.get("job_name", ""), targets))
        jobs_by_config[rel] = jobs
        all_targets = [target for _, targets in jobs for target in targets]
        if expected_targets[rel] not in all_targets:
            raise SystemExit(f"FAIL: {rel} must scrape {expected_targets[rel]!r}, got {all_targets}")
        if "run_id" in read(rel):
            raise SystemExit(f"FAIL: {rel} references a run identifier")
    # The full-backend job must keep the `activity-descriptor-api.*` prefix so
    # the existing dashboards/alerts work without modification.
    server_jobs = jobs_by_config["deploy/prometheus/prometheus.server.yml"]
    if not any(job.startswith("activity-descriptor-api") for job, _ in server_jobs):
        raise SystemExit(
            "FAIL: prometheus.server.yml job must match the activity-descriptor-api.* "
            "pattern used by the dashboards/alerts"
        )
    # Each config must not scrape a target that belongs to another mode.
    foreign = {
        "deploy/prometheus/prometheus.server.yml": ("api:8080", "host.docker.internal:8080"),
        "deploy/prometheus/prometheus.host.yml": ("api:8080", "server:8080"),
        "deploy/prometheus/prometheus.yml": ("server:8080", "host.docker.internal:8080"),
    }
    for rel, forbidden in foreign.items():
        targets = [target for _, targets in jobs_by_config[rel] for target in targets]
        for target in forbidden:
            if target in targets:
                raise SystemExit(f"FAIL: {rel} must not scrape the foreign target {target!r}")
    if "run_id" in read("deploy/prometheus/alerts.yml"):
        raise SystemExit("FAIL: deploy/prometheus/alerts.yml references a run identifier")
    print("prometheus ok: three scrape modes (api/host/server), alerts loaded, no run_id label")


def iter_exprs(node: object):
    """Yield every ``expr`` value from a (possibly nested) dashboard panel tree."""
    if isinstance(node, dict):
        for key, value in node.items():
            if key == "expr" and isinstance(value, str):
                yield value
            else:
                yield from iter_exprs(value)
    elif isinstance(node, list):
        for item in node:
            yield from iter_exprs(item)


def metric_families(source: str) -> set[str]:
    """Collect the activity_descriptor_* families defined by a metrics package."""
    names = {f"activity_descriptor_{name}" for name in re.findall(r'newVec\("([a-z_]+)"', source)}
    names.update(re.findall(r"activity_descriptor_([a-z_]+)", source))
    names = {f"activity_descriptor_{name}" if not name.startswith("activity_descriptor_") else name for name in names}
    return {re.sub(r"_(?:bucket|sum|count)$", "", name) for name in names}


def check_dashboard() -> None:
    # Known families: HTTP/run metrics from internal/metrics plus the LLM/agent
    # metrics from internal/llm/metrics exposed by cmd/server on /metrics.
    known = metric_families(read("internal/metrics/metrics.go"))
    known |= metric_families(read("internal/llm/metrics/metrics.go"))

    dashboards = {
        "deploy/grafana/dashboards/activity-descriptor.json": (
            "activity-descriptor",
            (
                "activity_descriptor_http_requests_total",
                "activity_descriptor_http_request_duration_seconds",
                "activity_descriptor_http_in_flight_requests",
            ),
            (),
            ("latency", "error", "route", "in-flight"),
        ),
        "deploy/grafana/dashboards/activity-descriptor-business.json": (
            "activity-descriptor-business",
            (
                "activity_descriptor_http_requests_total",
                "activity_descriptor_http_request_duration_seconds",
            ),
            (
                "activity_descriptor_agent_calls_total",
                "activity_descriptor_agent_latency_seconds",
                "activity_descriptor_agent_tokens_total",
            ),
            ("http", "llm"),
        ),
    }

    total_panels = 0
    for rel, (uid, required_http, required_llm, required_titles) in dashboards.items():
        dashboard = json.loads(read(rel))
        if dashboard.get("uid") != uid:
            raise SystemExit(f"FAIL: {rel} uid must be {uid}")
        panels = dashboard.get("panels") or []
        if len(panels) < 8:
            raise SystemExit(f"FAIL: {rel} has {len(panels)} panels, expected >= 8")
        titles = " ".join(panel.get("title", "") for panel in panels).lower()
        for required in required_titles:
            if required not in titles:
                raise SystemExit(f"FAIL: {rel} is missing a {required!r} panel")
        if not any(panel.get("targets") for panel in panels):
            raise SystemExit(f"FAIL: {rel} has no panel queries")
        referenced: set[str] = set()
        for expr in iter_exprs(dashboard):
            if "run_id" in expr:
                raise SystemExit(f"FAIL: {rel} queries a run identifier label")
            referenced.update(re.findall(r"activity_descriptor_[a-z_]+", expr))
        # Histogram exposition adds _bucket/_sum/_count suffixes to the family.
        families = {re.sub(r"_(?:bucket|sum|count)$", "", name) for name in referenced}
        unknown = sorted(families - known)
        if unknown:
            raise SystemExit(f"FAIL: {rel} queries undefined metrics: {unknown}")
        for important in required_http:
            if important not in families:
                raise SystemExit(f"FAIL: {rel} must query {important}")
        for important in required_llm:
            if important not in families:
                raise SystemExit(f"FAIL: {rel} must query {important}")
        total_panels += len(panels)
    print(
        f"dashboards ok: {len(dashboards)} dashboards, {total_panels} panels; "
        "HTTP + agent/LLM families cross-checked against the Go metrics sources, no run_id label"
    )


def check_metrics_labels() -> None:
    """The /metrics exposition must never use run-level identifiers as labels."""
    for rel in ("internal/metrics/metrics.go", "internal/llm/metrics/metrics.go"):
        if re.search(r"run_id", read(rel)):
            raise SystemExit(f"FAIL: {rel} uses run_id in metric labels")
    labeled = read("internal/metrics/metrics.go")
    if "run_id" in "".join(re.findall(r"newVec\([^)]*\)", labeled)):
        raise SystemExit("FAIL: internal/metrics registers a run_id label")
    print("metrics labels ok: no run_id label in the Go metrics sources")


def check_vector_migrations() -> None:
    """The optional vector group must stay a separate, goose-managed history."""
    directory = os.path.join(ROOT, "migrations_optional/vector")
    files = sorted(name for name in os.listdir(directory) if name.endswith(".sql"))
    if len(files) < 2:
        raise SystemExit(f"FAIL: migrations_optional/vector has {len(files)} SQL migrations, expected >= 2")
    retrieval = read("migrations_optional/vector/20261109010000_vector_retrieval.sql")
    for marker in (
        "+goose Up",
        "CREATE EXTENSION IF NOT EXISTS vector",
        "CREATE TABLE source_fragments",
        "CREATE TABLE fragment_embeddings",
        "USING hnsw (embedding vector_cosine_ops)",
    ):
        if marker not in retrieval:
            raise SystemExit(f"FAIL: vector migration is missing {marker!r}")
    print(f"vector migrations ok: {len(files)} SQL files, pgvector tables + HNSW index")


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
    used_env = set()
    for _, workflow in workflows:
        used_env.update(re.findall(r"\$env\.([A-Z0-9_]+)", json.dumps(workflow)))
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
    check_nginx()
    check_prometheus()
    check_dashboard()
    check_metrics_labels()
    check_vector_migrations()
    check_web()
    check_workflows()
    check_required_files()
    check_markdown_links()
    check_secrets()
    print("deploy config OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
