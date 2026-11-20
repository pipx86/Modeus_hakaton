#!/usr/bin/env python3
"""Demo-day preflight for the Activity Descriptor stack.

Quick mode checks the live services (backend, frontend, Prometheus, Grafana,
Jaeger, PostgreSQL through the backend, JWT issuance, demo data, snapshots and
the expert review queue) and prints one PASS/WARN/FAIL line per check.

Full mode additionally drives a real interview -> analysis -> synthesis run
through the HTTP API: it creates a run, answers the six scripted questions,
waits for the pipeline, then verifies the result (status done, tree nodes,
final research message, snapshot, conflicts). The successful run is kept as a
demo backup on purpose.

The script is stdlib-only, cross-platform and never prints secrets. Exit code 0
means every check passed (warnings are allowed); exit code 1 means at least one
check failed; exit code 2 is an invalid command line.

Examples:
    python scripts/demo_preflight.py
    python scripts/demo_preflight.py --json
    python scripts/demo_preflight.py --full
    python scripts/demo_preflight.py --full --timeout 1200 --profession "Data Analyst"
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import hmac
import json
import os
import re
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

DEFAULT_BASE_URL = "http://127.0.0.1:8080"
DEFAULT_FRONTEND_URL = "http://127.0.0.1:5173"
DEFAULT_PROMETHEUS_URL = "http://127.0.0.1:9090"
DEFAULT_GRAFANA_URL = "http://127.0.0.1:3000"
DEFAULT_JAEGER_URL = "http://127.0.0.1:16686"
DEFAULT_PROFESSION = "Data Analyst"
DEFAULT_HTTP_TIMEOUT = 20.0
DEFAULT_PIPELINE_TIMEOUT = 900.0
DEFAULT_ANSWER_TIMEOUT = 240.0
DEFAULT_POLL_INTERVAL = 2.0
TOKEN_TTL_SECONDS = 3600

EXIT_OK = 0
EXIT_FAIL = 1

PASS = "PASS"
WARN = "WARN"
FAIL = "FAIL"

RUN_TERMINAL_STATUSES = {"done", "failed", "degraded"}

# Six scripted answers for the Data Analyst interview. They are specific (more
# than four words, no vague markers) so the interviewer does not ask
# clarification questions, and the pipeline gets grounded evidence.
SCRIPTED_ANSWERS: tuple[str, ...] = (
    "I am a data analyst in a product team and I own weekly revenue reporting and data quality checks.",
    "My main functions are collecting requirements, building dashboards in BI, and validating metrics against the source system.",
    "Every week I prepare the sales funnel report and investigate any discrepancies in the data.",
    "I export data with SQL queries, build a dashboard in BI, and compare the metrics with the source table.",
    "I also maintain the metrics dictionary and update it after every product release.",
    "I automate recurring data checks with Python scripts and document the results in Confluence for the team.",
)


class DemoAPIError(RuntimeError):
    """A service could not be reached or returned an unusable response."""


class PipelineError(RuntimeError):
    """The live demo pipeline could not be driven to a verified result."""


class PipelineTimeout(PipelineError):
    """The live demo pipeline did not finish before the configured timeout."""


@dataclass
class CheckResult:
    name: str
    status: str
    detail: str = ""
    hint: str = ""


@dataclass
class HttpResponse:
    status: int
    url: str
    body: bytes

    def text(self) -> str:
        return self.body.decode("utf-8", errors="replace")

    def json(self):
        try:
            return json.loads(self.body.decode("utf-8-sig"))
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise DemoAPIError(
                f"{self.url} returned non-JSON content (HTTP {self.status})"
            ) from exc


# --------------------------------------------------------------------------- #
# HTTP helpers
# --------------------------------------------------------------------------- #


def join_url(base_url: str, path: str) -> str:
    """Join a base URL and an API path without doubling or dropping slashes."""

    return base_url.rstrip("/") + "/" + path.lstrip("/")


def http_request(
    url: str,
    method: str = "GET",
    data: bytes | None = None,
    headers: dict[str, str] | None = None,
    timeout: float = DEFAULT_HTTP_TIMEOUT,
    max_body: int = 4 * 1024 * 1024,
) -> HttpResponse:
    """Perform one HTTP request and return the response even for 4xx/5xx.

    Connection-level failures are reported as DemoAPIError with an actionable
    message. Response bodies are capped so a stray endpoint cannot exhaust
    memory.
    """

    request = urllib.request.Request(url, data=data, method=method, headers=headers or {})
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            body = response.read(max_body)
            return HttpResponse(status=int(response.status), url=url, body=body)
    except urllib.error.HTTPError as exc:
        try:
            body = exc.read(max_body)
        except OSError:
            body = b""
        return HttpResponse(status=int(exc.code), url=url, body=body)
    except (urllib.error.URLError, TimeoutError, OSError) as exc:
        reason = getattr(exc, "reason", exc)
        raise DemoAPIError(f"cannot reach {url}: {reason}") from exc


class ApiClient:
    """Minimal JSON client for the protected /api/v1 endpoints."""

    def __init__(self, base_url: str, token: str | None = None, timeout: float = DEFAULT_HTTP_TIMEOUT):
        self.base_url = base_url
        self.token = token
        self.timeout = timeout

    def request(self, method: str, path: str, payload=None, authenticated: bool = True) -> HttpResponse:
        headers = {
            "Accept": "application/json",
            "User-Agent": "activity-descriptor-demo-preflight/1.0",
        }
        data = None
        if payload is not None:
            data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
            headers["Content-Type"] = "application/json"
        if authenticated and self.token:
            headers["Authorization"] = "Bearer " + self.token
        return http_request(
            join_url(self.base_url, path),
            method=method,
            data=data,
            headers=headers,
            timeout=self.timeout,
        )

    def get(self, path: str) -> HttpResponse:
        return self.request("GET", path)

    def post(self, path: str, payload) -> HttpResponse:
        return self.request("POST", path, payload)

    def delete(self, path: str) -> HttpResponse:
        return self.request("DELETE", path)

    def get_json(self, path: str):
        response = self.get(path)
        if response.status != 200:
            raise DemoAPIError(
                f"GET {path} -> HTTP {response.status} {error_code(response)}".strip()
            )
        return response.json()


def error_code(response: HttpResponse) -> str:
    """Return the stable API error code/message without echoing a payload."""

    try:
        payload = response.json()
    except DemoAPIError:
        return clip(response.text(), 120)
    if isinstance(payload, dict):
        for key in ("code", "message"):
            value = payload.get(key)
            if isinstance(value, str) and value.strip():
                return clip(value.strip(), 120)
    return ""


def clip(text: str, limit: int = 160) -> str:
    """Trim free-form text to one bounded line for progress output."""

    single_line = " ".join(str(text).split())
    if len(single_line) <= limit:
        return single_line
    return single_line[: max(0, limit - 3)] + "..."


# --------------------------------------------------------------------------- #
# Environment and JWT helpers
# --------------------------------------------------------------------------- #


def parse_dotenv_text(text: str) -> dict[str, str]:
    """Parse KEY=VALUE lines from a .env file without printing any values."""

    values: dict[str, str] = {}
    for raw_line in text.splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        if line.lower().startswith("export "):
            line = line[len("export ") :].strip()
        if "=" not in line:
            continue
        key, value = line.split("=", 1)
        key = key.strip()
        value = value.strip()
        if not key:
            continue
        if len(value) >= 2 and value[0] == value[-1] and value[0] in ('"', "'"):
            value = value[1:-1]
        elif " #" in value:
            value = value.split(" #", 1)[0].rstrip()
        values[key] = value
    return values


def load_env_files(root: Path = ROOT) -> dict[str, str]:
    """Merge optional .env/.env.local files; missing files are not an error."""

    merged: dict[str, str] = {}
    for name in (".env", ".env.local"):
        path = root / name
        try:
            merged.update(parse_dotenv_text(path.read_text(encoding="utf-8")))
        except OSError:
            continue
    return merged


def resolve_env(name: str, dotenv: dict[str, str]) -> str:
    """Process environment wins over .env; values are never printed."""

    value = os.environ.get(name)
    if value is not None and value.strip():
        return value.strip()
    return (dotenv.get(name) or "").strip()


def b64url_encode(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode("ascii")


def mint_token(secret: str, subject: str = "demo-preflight", role: str = "admin", ttl_seconds: int = TOKEN_TTL_SECONDS) -> str:
    """Mint the same HS256 JWT the Go backend expects (stdlib only)."""

    if not secret:
        raise ValueError("JWT secret is empty")
    now = int(time.time())
    header = {"alg": "HS256", "typ": "JWT"}
    claims = {"sub": subject, "role": role, "iat": now, "exp": now + ttl_seconds}
    signing_input = ".".join(
        (
            b64url_encode(json.dumps(header, separators=(",", ":")).encode("utf-8")),
            b64url_encode(json.dumps(claims, separators=(",", ":")).encode("utf-8")),
        )
    )
    signature = hmac.new(secret.encode("utf-8"), signing_input.encode("ascii"), hashlib.sha256).digest()
    return signing_input + "." + b64url_encode(signature)


def resolve_token(cli_token: str | None, dotenv: dict[str, str]) -> tuple[str, str]:
    """Return (token, human-readable source). The token itself is never printed."""

    if cli_token and cli_token.strip():
        return cli_token.strip(), "--token"
    for env_name in ("DEMO_TOKEN", "API_TOKEN"):
        value = (os.environ.get(env_name) or "").strip()
        if value:
            return value, f"${env_name}"
    secret = resolve_env("JWT_SECRET", dotenv)
    if secret:
        return mint_token(secret), "JWT_SECRET"
    raise DemoAPIError(
        "no JWT available: pass --token, export DEMO_TOKEN, or export JWT_SECRET "
        "(the same value the backend uses)"
    )


# --------------------------------------------------------------------------- #
# Small data helpers
# --------------------------------------------------------------------------- #


def parse_iso_datetime(value) -> datetime | None:
    """Parse the API timestamps (RFC 3339 with an offset or trailing Z)."""

    if not value:
        return None
    text = str(value).strip()
    if text.endswith(("Z", "z")):
        text = text[:-1] + "+00:00"
    try:
        parsed = datetime.fromisoformat(text)
    except ValueError:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed.astimezone(timezone.utc)


def format_duration(seconds: float) -> str:
    total = int(max(0.0, seconds))
    days, remainder = divmod(total, 86400)
    hours, remainder = divmod(remainder, 3600)
    minutes, secs = divmod(remainder, 60)
    if days:
        return f"{days}d {hours}h"
    if hours:
        return f"{hours}h {minutes}m"
    if minutes:
        return f"{minutes}m"
    return f"{secs}s"


def format_age(started_at, now: datetime | None = None) -> str:
    started = parse_iso_datetime(started_at)
    if started is None:
        return "?"
    reference = now or datetime.now(timezone.utc)
    return format_duration((reference - started).total_seconds())


def llm_evidence(dotenv: dict[str, str], metrics_text: str, environ=None) -> tuple[bool, str]:
    """Report whether an LLM provider is configured without printing secrets.

    Sources, in order: the shell environment, the optional .env file, and the
    backend /metrics exposition (interviewer model calls prove the backend
    process itself has a provider configured).
    """

    environment = os.environ if environ is None else environ
    base_url = (environment.get("LLM_BASE_URL") or dotenv.get("LLM_BASE_URL") or "").strip()
    api_key = (environment.get("LLM_API_KEY") or dotenv.get("LLM_API_KEY") or "").strip()
    key_file = (environment.get("LLM_API_KEY_FILE") or dotenv.get("LLM_API_KEY_FILE") or "").strip()
    interviewer_calls = len(
        re.findall(r'activity_descriptor_agent_calls_total\{[^}]*agent="interviewer"', metrics_text or "")
    )
    key_file_ok = False
    if key_file:
        try:
            key_file_ok = Path(key_file).expanduser().is_file()
        except OSError:
            key_file_ok = False
    if base_url and (api_key or key_file_ok):
        detail = (
            "LLM_BASE_URL=set; "
            f"LLM_API_KEY={'set' if api_key else 'unset'}; "
            f"LLM_API_KEY_FILE={'set' if key_file else 'unset'}"
        )
        return True, detail
    if interviewer_calls:
        return True, "backend /metrics reports interviewer model calls (provider configured in the backend process)"
    return False, (
        "LLM_BASE_URL/LLM_API_KEY are not visible in the shell or .env and "
        "/metrics has no interviewer model calls"
    )


def profession_has_stepik_courses(profession: str) -> bool:
    """Mirror the interviewer catalogue check without importing Go code."""

    normalized = (profession or "").lower().replace("ё", "е")
    markers = (
        "analyst",
        "аналитик",
        "данных",
        "devops",
        "девопс",
        "backend",
        "разработчик",
        "бэкенд",
        "бекенд",
    )
    return any(marker in normalized for marker in markers)


# --------------------------------------------------------------------------- #
# Quick checks
# --------------------------------------------------------------------------- #


def _check_service(check_name: str, validator, checks: list[CheckResult], args) -> None:
    try:
        response = http_request(validator.url, timeout=args.http_timeout)
    except DemoAPIError as exc:
        checks.append(CheckResult(check_name, FAIL, str(exc), validator.hint))
        return
    status, detail = validator.validate(response)
    checks.append(CheckResult(check_name, status, detail, "" if status == PASS else validator.hint))


class _ServiceValidator:
    def __init__(self, url: str, hint: str, validate):
        self.url = url
        self.hint = hint
        self.validate = validate


def _validate_frontend(response: HttpResponse) -> tuple[str, str]:
    if response.status != 200:
        return FAIL, f"GET {response.url} -> HTTP {response.status}"
    marker = 'id="root"' in response.text()
    detail = f"GET {response.url} -> 200 ({len(response.body)} bytes, demo marker {'found' if marker else 'missing'})"
    return (PASS if marker else WARN), detail


def _validate_prometheus(response: HttpResponse) -> tuple[str, str]:
    if response.status != 200:
        return FAIL, f"GET {response.url} -> HTTP {response.status}"
    ready = "ready" in response.text().lower()
    return (PASS if ready else WARN), f"GET {response.url} -> 200 ({'ready' if ready else 'not reporting ready'})"


def _validate_grafana(response: HttpResponse) -> tuple[str, str]:
    if response.status != 200:
        return FAIL, f"GET {response.url} -> HTTP {response.status}"
    try:
        payload = response.json()
        database = str(payload.get("database", "?")) if isinstance(payload, dict) else "?"
        version = str(payload.get("version", "?")) if isinstance(payload, dict) else "?"
    except DemoAPIError:
        return WARN, f"GET {response.url} -> 200 (health payload is not JSON)"
    return PASS, f"GET {response.url} -> 200 (database={database}, version={version})"


def _validate_jaeger(response: HttpResponse) -> tuple[str, str]:
    if response.status != 200:
        return FAIL, f"GET {response.url} -> HTTP {response.status}"
    ui = "jaeger" in response.text().lower()
    return PASS, f"GET {response.url} -> 200 ({'Jaeger UI' if ui else 'UI served'})"


def _docker_postgres_check(checks: list[CheckResult], enabled: bool) -> None:
    if not enabled:
        checks.append(CheckResult("docker postgres (optional)", WARN, "skipped with --skip-docker"))
        return
    docker = shutil.which("docker")
    if not docker:
        checks.append(
            CheckResult(
                "docker postgres (optional)",
                WARN,
                "docker CLI not found; PostgreSQL was checked through the backend API only",
            )
        )
        return
    command = [docker, "ps", "--filter", "name=postgres", "--format", "{{.Names}}|{{.Status}}"]
    try:
        completed = subprocess.run(command, capture_output=True, text=True, timeout=15, check=False)
    except (OSError, subprocess.TimeoutExpired) as exc:
        checks.append(
            CheckResult(
                "docker postgres (optional)",
                WARN,
                f"docker ps could not run ({type(exc).__name__}); the API check remains authoritative",
            )
        )
        return
    if completed.returncode != 0:
        first_error = clip((completed.stderr or "").strip().splitlines()[0] if completed.stderr.strip() else "")
        checks.append(
            CheckResult(
                "docker postgres (optional)",
                WARN,
                f"docker CLI found but the daemon is not reachable ({first_error or 'no stderr'}); "
                "the API check remains authoritative",
            )
        )
        return
    containers = [line.strip() for line in (completed.stdout or "").splitlines() if line.strip()]
    if not containers:
        checks.append(
            CheckResult(
                "docker postgres (optional)",
                WARN,
                "no running container matching 'postgres'; the backend may use an external PostgreSQL",
            )
        )
        return
    detail = "; ".join(containers)
    status = PASS if any("up" in item.lower() for item in containers) else WARN
    checks.append(CheckResult("docker postgres (optional)", status, clip(detail, 200)))


def run_quick_checks(args) -> tuple[list[CheckResult], dict]:
    """Run the deterministic, read-only preflight checks."""

    checks: list[CheckResult] = []
    dotenv = load_env_files()
    context: dict = {
        "dotenv": dotenv,
        "token": None,
        "client": None,
        "runs": None,
        "completed_run": None,
        "metrics_text": "",
        "backend_ok": False,
        "jwt_ok": False,
        "db_ok": False,
        "llm_ok": False,
    }

    # Backend health -------------------------------------------------------- #
    try:
        response = http_request(join_url(args.base_url, "/api/v1/health"), timeout=args.http_timeout)
        if response.status != 200:
            checks.append(
                CheckResult(
                    "backend health",
                    FAIL,
                    f"GET /api/v1/health -> HTTP {response.status}",
                    "start the API: go run ./cmd/server (needs DATABASE_URL and JWT_SECRET)",
                )
            )
        else:
            payload = None
            try:
                payload = response.json()
            except DemoAPIError:
                payload = None
            if isinstance(payload, dict) and payload.get("status") == "ok":
                checks.append(
                    CheckResult(
                        "backend health",
                        PASS,
                        f"GET /api/v1/health -> 200 {clip(response.text(), 80)}",
                    )
                )
                context["backend_ok"] = True
            else:
                checks.append(
                    CheckResult(
                        "backend health",
                        FAIL,
                        f"GET /api/v1/health -> 200 but the body is not {{\"status\":\"ok\"}}: {clip(response.text(), 120)}",
                        "the API answered with an unexpected payload; check logs/app.log",
                    )
                )
    except DemoAPIError as exc:
        checks.append(
            CheckResult(
                "backend health",
                FAIL,
                str(exc),
                "start the API: go run ./cmd/server (needs DATABASE_URL and JWT_SECRET)",
            )
        )

    # Backend metrics ------------------------------------------------------- #
    try:
        response = http_request(join_url(args.base_url, "/metrics"), timeout=args.http_timeout)
        metrics_text = response.text() if response.status == 200 else ""
        context["metrics_text"] = metrics_text
        if response.status != 200:
            checks.append(
                CheckResult(
                    "backend metrics",
                    FAIL,
                    f"GET /metrics -> HTTP {response.status}",
                    "GET /metrics must answer 200 for the Prometheus demo; check the backend route",
                )
            )
        elif "activity_descriptor_" in metrics_text:
            checks.append(
                CheckResult(
                    "backend metrics",
                    PASS,
                    f"GET /metrics -> 200 ({len(response.body)} bytes, activity_descriptor_* exposed)",
                )
            )
        else:
            checks.append(
                CheckResult(
                    "backend metrics",
                    FAIL,
                    "GET /metrics -> 200 but no activity_descriptor_* metrics",
                    "check cmd/server/metrics wiring; Grafana panels need these metrics",
                )
            )
    except DemoAPIError as exc:
        checks.append(
            CheckResult(
                "backend metrics",
                FAIL,
                str(exc),
                "GET /metrics must answer 200 for the Prometheus demo",
            )
        )

    # External services ----------------------------------------------------- #
    _check_service(
        "frontend",
        _ServiceValidator(
            args.frontend_url,
            "start the frontend: cd frontend; npm run dev -- --host 127.0.0.1 --port 5173",
            _validate_frontend,
        ),
        checks,
        args,
    )
    _check_service(
        "prometheus",
        _ServiceValidator(
            join_url(args.prometheus_url, "/-/ready"),
            "start monitoring: docker compose -f docker-compose.yml --profile monitoring up -d prometheus grafana jaeger",
            _validate_prometheus,
        ),
        checks,
        args,
    )
    _check_service(
        "grafana",
        _ServiceValidator(
            join_url(args.grafana_url, "/api/health"),
            "start monitoring: docker compose -f docker-compose.yml --profile monitoring up -d prometheus grafana jaeger",
            _validate_grafana,
        ),
        checks,
        args,
    )
    _check_service(
        "jaeger",
        _ServiceValidator(
            args.jaeger_url,
            "start monitoring: docker compose -f docker-compose.yml --profile monitoring up -d prometheus grafana jaeger",
            _validate_jaeger,
        ),
        checks,
        args,
    )

    _docker_postgres_check(checks, enabled=not args.skip_docker)

    # JWT issuance ---------------------------------------------------------- #
    token = None
    token_source = ""
    try:
        token, token_source = resolve_token(args.token, dotenv)
        context["token"] = token
    except DemoAPIError as exc:
        checks.append(
            CheckResult(
                "jwt issuance",
                FAIL,
                str(exc),
                "export JWT_SECRET with the same value the backend uses, or pass --token; "
                "issue one with: go run ./cmd/local-token -role admin",
            )
        )
    if token:
        client = ApiClient(args.base_url, token, args.http_timeout)
        context["client"] = client
        try:
            response = client.get("/api/v1/users")
            if response.status == 200 and isinstance(response.json(), list):
                checks.append(
                    CheckResult(
                        "jwt issuance",
                        PASS,
                        f"token source: {token_source}; GET /api/v1/users -> 200 (admin token accepted)",
                    )
                )
                context["jwt_ok"] = True
            elif response.status in (401, 403):
                checks.append(
                    CheckResult(
                        "jwt issuance",
                        FAIL,
                        f"backend rejected the token: GET /api/v1/users -> HTTP {response.status} {error_code(response)}".strip(),
                        "JWT_SECRET must match the backend and the token must carry role=admin; "
                        "re-issue it: go run ./cmd/local-token -role admin",
                    )
                )
            else:
                checks.append(
                    CheckResult(
                        "jwt issuance",
                        FAIL,
                        f"GET /api/v1/users -> HTTP {response.status} {error_code(response)}".strip(),
                        "check the backend route and logs/app.log",
                    )
                )
        except DemoAPIError as exc:
            checks.append(CheckResult("jwt issuance", FAIL, str(exc), "check backend health first"))

    # PostgreSQL through the runs API --------------------------------------- #
    client = context["client"]
    if client is not None:
        try:
            response = client.get("/api/v1/runs")
            if response.status == 200 and isinstance(response.json(), list):
                runs = response.json()
                context["runs"] = runs
                context["db_ok"] = True
                checks.append(
                    CheckResult(
                        "postgresql (via /api/v1/runs)",
                        PASS,
                        f"GET /api/v1/runs -> 200, {len(runs)} run(s) (PostgreSQL reachable through the backend)",
                    )
                )
            elif response.status == 501:
                checks.append(
                    CheckResult(
                        "postgresql (via /api/v1/runs)",
                        FAIL,
                        "GET /api/v1/runs -> 501 (in-memory store, no PostgreSQL)",
                        "start the real backend: go run ./cmd/server with DATABASE_URL pointing at PostgreSQL",
                    )
                )
            elif response.status == 500:
                checks.append(
                    CheckResult(
                        "postgresql (via /api/v1/runs)",
                        FAIL,
                        f"GET /api/v1/runs -> 500 {error_code(response)}".strip(),
                        "PostgreSQL is not reachable: docker ps; docker compose -f docker-compose.yml up -d postgres; "
                        "check DATABASE_URL and migrations",
                    )
                )
            else:
                checks.append(
                    CheckResult(
                        "postgresql (via /api/v1/runs)",
                        FAIL,
                        f"GET /api/v1/runs -> HTTP {response.status} {error_code(response)}".strip(),
                        "check backend logs/app.log and the DATABASE_URL",
                    )
                )
        except DemoAPIError as exc:
            checks.append(
                CheckResult(
                    "postgresql (via /api/v1/runs)",
                    FAIL,
                    str(exc),
                    "check backend health first",
                )
            )

    # LLM configuration ----------------------------------------------------- #
    llm_ok, llm_detail = llm_evidence(dotenv, context["metrics_text"])
    context["llm_ok"] = llm_ok
    if llm_ok:
        checks.append(CheckResult("llm configuration", PASS, llm_detail))
    else:
        checks.append(
            CheckResult(
                "llm configuration",
                WARN,
                llm_detail,
                "the full pipeline needs a provider; export the same LLM_* variables as the backend "
                "or pass --force to attempt the smoke run anyway",
            )
        )

    # Demo data: completed run, tree, snapshots, review queue --------------- #
    completed_run = None
    if isinstance(context["runs"], list):
        done_runs = [
            run for run in context["runs"] if isinstance(run, dict) and run.get("status") == "done"
        ]
        if done_runs:
            completed_run = done_runs[0]
            context["completed_run"] = completed_run
            checks.append(
                CheckResult(
                    "completed run",
                    PASS,
                    f"{completed_run.get('id')} "
                    f"({completed_run.get('profession')}, finished {completed_run.get('finished_at')})",
                )
            )
        else:
            checks.append(
                CheckResult(
                    "completed run",
                    FAIL,
                    "no run with status=done in GET /api/v1/runs",
                    "create one with: python scripts/demo_preflight.py --full "
                    "(or complete an interview in the frontend)",
                )
            )
    if completed_run is not None and client is not None:
        run_id = completed_run.get("id")
        try:
            response = client.get(f"/api/v1/runs/{run_id}/tree")
            payload = response.json() if response.status == 200 else None
            nodes = payload.get("nodes", []) if isinstance(payload, dict) else payload
            if response.status == 200 and isinstance(nodes, list):
                if nodes:
                    checks.append(
                        CheckResult(
                            "completed run tree",
                            PASS,
                            f"GET /api/v1/runs/{run_id}/tree -> 200, {len(nodes)} node(s)",
                        )
                    )
                else:
                    checks.append(
                        CheckResult(
                            "completed run tree",
                            WARN,
                            f"tree for {run_id} is empty (the run may have completed without an LLM finalizer)",
                            "rerun the full smoke: python scripts/demo_preflight.py --full",
                        )
                    )
            else:
                checks.append(
                    CheckResult(
                        "completed run tree",
                        FAIL,
                        f"GET /api/v1/runs/{run_id}/tree -> HTTP {response.status} {error_code(response)}".strip(),
                        "check the backend tree endpoint and logs/app.log",
                    )
                )
        except DemoAPIError as exc:
            checks.append(CheckResult("completed run tree", FAIL, str(exc), "check backend health first"))

    # Snapshots ------------------------------------------------------------- #
    if completed_run is not None and client is not None:
        run_id = completed_run.get("id")
        try:
            response = client.get(f"/api/v1/runs/{run_id}/snapshots")
            if response.status == 200 and isinstance(response.json(), list):
                snapshots = response.json()
                if snapshots:
                    checks.append(
                        CheckResult(
                            "snapshots",
                            PASS,
                            f"GET /api/v1/runs/{run_id}/snapshots -> 200, {len(snapshots)} snapshot(s)",
                        )
                    )
                else:
                    checks.append(
                        CheckResult(
                            "snapshots",
                            WARN,
                            f"snapshots endpoint works, but the latest completed run {run_id} has none",
                            "older runs may predate the automatic snapshot; run --full to create a run with a snapshot",
                        )
                    )
            elif response.status == 501:
                checks.append(
                    CheckResult(
                        "snapshots",
                        FAIL,
                        "GET /snapshots -> 501 (snapshots require PostgreSQL)",
                        "start cmd/server with PostgreSQL",
                    )
                )
            else:
                checks.append(
                    CheckResult(
                        "snapshots",
                        FAIL,
                        f"GET /api/v1/runs/{run_id}/snapshots -> HTTP {response.status} {error_code(response)}".strip(),
                        "check the backend snapshots endpoint and logs/app.log",
                    )
                )
        except DemoAPIError as exc:
            checks.append(CheckResult("snapshots", FAIL, str(exc), "check backend health first"))

    # Review queue ---------------------------------------------------------- #
    if client is not None:
        try:
            response = client.get("/api/v1/reviews/evidence?status=pending&limit=1")
            if response.status == 200 and isinstance(response.json(), list):
                checks.append(
                    CheckResult(
                        "review queue",
                        PASS,
                        f"GET /api/v1/reviews/evidence -> 200, {len(response.json())} item(s) on the first page",
                    )
                )
            elif response.status == 501:
                checks.append(
                    CheckResult(
                        "review queue",
                        FAIL,
                        "GET /reviews/evidence -> 501 (the expert review queue requires PostgreSQL)",
                        "start cmd/server with PostgreSQL",
                    )
                )
            else:
                checks.append(
                    CheckResult(
                        "review queue",
                        FAIL,
                        f"GET /api/v1/reviews/evidence -> HTTP {response.status} {error_code(response)}".strip(),
                        "check the review queue endpoint and logs/app.log",
                    )
                )
        except DemoAPIError as exc:
            checks.append(CheckResult("review queue", FAIL, str(exc), "check backend health first"))

    return checks, context


# --------------------------------------------------------------------------- #
# Full pipeline smoke
# --------------------------------------------------------------------------- #


def _emit(args, message: str) -> None:
    """Progress goes to stderr in --json mode so stdout stays machine-readable."""

    print(message, file=sys.stderr if args.json else sys.stdout, flush=True)


def _run_status(client: ApiClient, run_id: str) -> dict:
    response = client.get(f"/api/v1/runs/{run_id}")
    if response.status != 200:
        raise PipelineError(f"GET /api/v1/runs/{run_id} -> HTTP {response.status} {error_code(response)}".strip())
    payload = response.json()
    if not isinstance(payload, dict):
        raise PipelineError(f"GET /api/v1/runs/{run_id} returned an unexpected payload")
    return payload


def _messages(client: ApiClient, run_id: str) -> list:
    response = client.get(f"/api/v1/runs/{run_id}/messages")
    if response.status == 503:
        raise PipelineError("the backend answered 503 CHAT_DISABLED: chat is not configured")
    if response.status != 200:
        raise PipelineError(
            f"GET /api/v1/runs/{run_id}/messages -> HTTP {response.status} {error_code(response)}".strip()
        )
    payload = response.json()
    if not isinstance(payload, list):
        raise PipelineError(f"GET /api/v1/runs/{run_id}/messages returned an unexpected payload")
    return payload


def _wait_for_question(
    client: ApiClient,
    run_id: str,
    minimum_agent_messages: int,
    deadline: float,
    poll: float,
) -> tuple[int, list]:
    """Wait until the interviewer has published at least N agent messages.

    The welcome message is the first agent message, so N=2 means "the first
    question is ready" and N=prev+1 means "the next question is ready".
    """

    while True:
        run = _run_status(client, run_id)
        status = str(run.get("status") or "")
        if status in RUN_TERMINAL_STATUSES:
            raise PipelineError(
                f"run finished with status '{status}' before the interview completed (stage={run.get('stage')})"
            )
        messages = _messages(client, run_id)
        agent_count = sum(1 for message in messages if isinstance(message, dict) and message.get("role") == "agent")
        if agent_count >= minimum_agent_messages:
            return agent_count, messages
        if time.monotonic() >= deadline:
            raise PipelineTimeout(
                "the interviewer did not publish the next question in time "
                f"(agent messages: {agent_count}/{minimum_agent_messages})"
            )
        time.sleep(poll)


def _wait_for_terminal(client: ApiClient, run_id: str, deadline: float, poll: float) -> tuple[str, dict]:
    last_run: dict = {}
    while True:
        last_run = _run_status(client, run_id)
        status = str(last_run.get("status") or "")
        if status in RUN_TERMINAL_STATUSES:
            return status, last_run
        if time.monotonic() >= deadline:
            raise PipelineTimeout(
                f"the run stayed in status '{status}' (stage={last_run.get('stage')}) until the overall timeout"
            )
        time.sleep(poll)


def _final_agent_message(messages: list) -> dict | None:
    for message in reversed(messages):
        if isinstance(message, dict) and message.get("role") == "agent":
            return message
    return None


def run_full_pipeline(args, context: dict, checks: list[CheckResult]) -> dict:
    """Create one real run, answer six questions and verify the pipeline result."""

    started = time.monotonic()
    summary: dict = {
        "requested": True,
        "run_id": None,
        "status": None,
        "answers_sent": 0,
        "questions_seen": 0,
        "tree_nodes": 0,
        "snapshots": 0,
        "conflicts": 0,
        "messages": 0,
        "final_message": False,
        "elapsed_s": 0.0,
        "kept": True,
    }
    client: ApiClient | None = context.get("client")
    if client is None:
        checks.append(
            CheckResult(
                "full: prerequisites",
                FAIL,
                "no authenticated API client (backend health or JWT check failed)",
                "fix the failing backend/JWT checks first; --full needs a working stack",
            )
        )
        return summary
    if not context.get("llm_ok") and not args.force:
        checks.append(
            CheckResult(
                "full: LLM availability",
                FAIL,
                "no evidence that the backend has an LLM provider configured",
                "export the same LLM_* variables as the backend, or pass --force to try anyway "
                "(a run without an LLM completes without a tree)",
            )
        )
        return summary

    # Create the run -------------------------------------------------------- #
    try:
        response = client.post("/api/v1/runs", {"profession": args.profession})
    except DemoAPIError as exc:
        checks.append(CheckResult("full: create run", FAIL, str(exc), "check backend health and logs/app.log"))
        return summary
    if response.status != 201:
        checks.append(
            CheckResult(
                "full: create run",
                FAIL,
                f"POST /api/v1/runs -> HTTP {response.status} {error_code(response)}".strip(),
                "the backend could not create a run: check cmd/server (PostgreSQL, LLM) and logs/app.log",
            )
        )
        return summary
    try:
        created = response.json()
    except DemoAPIError as exc:
        checks.append(CheckResult("full: create run", FAIL, str(exc), "the backend returned a non-JSON run payload"))
        return summary
    run_id = str((created or {}).get("id") or "")
    if not run_id:
        checks.append(
            CheckResult(
                "full: create run",
                FAIL,
                f"POST /api/v1/runs -> 201 but no run id: {clip(response.text(), 160)}",
                "check the create-run handler and logs/app.log",
            )
        )
        return summary
    summary["run_id"] = run_id
    checks.append(
        CheckResult(
            "full: create run",
            PASS,
            f"POST /api/v1/runs -> 201, run {run_id} (profession={args.profession!r}); kept as a demo backup",
        )
    )
    _emit(args, f"Created demo run {run_id}; driving the interview with {len(SCRIPTED_ANSWERS)} scripted answers...")

    deadline = time.monotonic() + args.timeout
    try:
        step_deadline = min(deadline, time.monotonic() + args.answer_timeout)
        agent_count, _ = _wait_for_question(client, run_id, 2, step_deadline, args.poll)
        summary["questions_seen"] = 1
        # chat.Post is only accepted once the question is fully published; the
        # small delay avoids racing the interviewer's "asking" flag.
        time.sleep(0.5)
        for index, answer in enumerate(SCRIPTED_ANSWERS, start=1):
            response = client.post(f"/api/v1/runs/{run_id}/messages", {"content": answer})
            if response.status != 201:
                raise PipelineError(
                    f"answer {index}/{len(SCRIPTED_ANSWERS)}: POST /messages -> "
                    f"HTTP {response.status} {error_code(response)}".strip()
                )
            summary["answers_sent"] = index
            _emit(args, f"  answer {index}/{len(SCRIPTED_ANSWERS)} accepted")
            if index < len(SCRIPTED_ANSWERS):
                step_deadline = min(deadline, time.monotonic() + args.answer_timeout)
                agent_count, _ = _wait_for_question(client, run_id, agent_count + 1, step_deadline, args.poll)
                summary["questions_seen"] = index + 1
                time.sleep(0.5)
        _emit(args, "  interview finished; waiting for analysis and synthesis (this can take several minutes)...")
        status, run_payload = _wait_for_terminal(client, run_id, deadline, args.poll)
        summary["status"] = status
    except PipelineTimeout as exc:
        summary["status"] = "timeout"
        summary["elapsed_s"] = round(time.monotonic() - started, 1)
        checks.append(
            CheckResult(
                "full: pipeline",
                FAIL,
                f"run {run_id}: {exc}",
                f"run {run_id} was kept; raise --timeout, inspect logs/app.log and the LLM provider, then retry",
            )
        )
        return summary
    except (DemoAPIError, PipelineError) as exc:
        summary["status"] = "error"
        summary["elapsed_s"] = round(time.monotonic() - started, 1)
        checks.append(
            CheckResult(
                "full: pipeline",
                FAIL,
                f"run {run_id}: {exc}",
                f"run {run_id} was kept; inspect logs/app.log and retry",
            )
        )
        return summary

    elapsed = round(time.monotonic() - started, 1)
    summary["elapsed_s"] = elapsed

    # Status ---------------------------------------------------------------- #
    if status == "done":
        checks.append(
            CheckResult(
                "full: pipeline status",
                PASS,
                f"run {run_id}: status=done (stage={run_payload.get('stage')}) in {format_duration(elapsed)}",
            )
        )
    else:
        checks.append(
            CheckResult(
                "full: pipeline status",
                FAIL,
                f"run {run_id}: status={status} (stage={run_payload.get('stage')}) after {format_duration(elapsed)}",
                "the LLM finalizer failed: inspect logs/app.log and the provider; the run was kept for inspection",
            )
        )

    # Interview answers ----------------------------------------------------- #
    if summary["answers_sent"] == len(SCRIPTED_ANSWERS):
        checks.append(
            CheckResult(
                "full: interview answers",
                PASS,
                f"{summary['answers_sent']}/{len(SCRIPTED_ANSWERS)} scripted answers accepted by POST /messages",
            )
        )
    else:
        checks.append(
            CheckResult(
                "full: interview answers",
                FAIL,
                f"only {summary['answers_sent']}/{len(SCRIPTED_ANSWERS)} answers were accepted",
                "the interview ended early; check logs/app.log",
            )
        )

    # Tree ------------------------------------------------------------------ #
    try:
        response = client.get(f"/api/v1/runs/{run_id}/tree")
        payload = response.json() if response.status == 200 else None
        nodes = payload.get("nodes", []) if isinstance(payload, dict) else payload
        if response.status == 200 and isinstance(nodes, list):
            summary["tree_nodes"] = len(nodes)
            if nodes:
                checks.append(
                    CheckResult("full: tree nodes", PASS, f"GET /tree -> 200, {len(nodes)} node(s)")
                )
            else:
                checks.append(
                    CheckResult(
                        "full: tree nodes",
                        FAIL,
                        "GET /tree -> 200 but 0 nodes",
                        "the pipeline completed without the analyst/synthesizer: the backend has no LLM finalizer "
                        "or the provider failed; check logs/app.log",
                    )
                )
        else:
            checks.append(
                CheckResult(
                    "full: tree nodes",
                    FAIL,
                    f"GET /tree -> HTTP {response.status} {error_code(response)}".strip(),
                    "check the tree endpoint and logs/app.log",
                )
            )
    except DemoAPIError as exc:
        checks.append(CheckResult("full: tree nodes", FAIL, str(exc), "check backend health"))

    # Final research message -------------------------------------------------- #
    try:
        messages = _messages(client, run_id)
        summary["messages"] = len(messages)
        final_message = _final_agent_message(messages)
        content = str((final_message or {}).get("content") or "")
        if final_message is None:
            checks.append(
                CheckResult(
                    "full: final message",
                    FAIL,
                    "the interview session has no agent messages",
                    "check logs/app.log; a backend restart drops the in-memory chat session",
                )
            )
        else:
            summary["final_message"] = "Интервью завершено" in content and "Дерево" in content
            checks.append(CheckResult(
                "full: final message",
                PASS if summary["final_message"] else FAIL,
                "research closing message found" if summary["final_message"] else "research closing message missing",
            ))
    except (DemoAPIError, PipelineError) as exc:
        checks.append(
            CheckResult(
                "full: final message",
                FAIL,
                str(exc),
                "a backend restart drops the in-memory chat session; check logs/app.log",
            )
        )

    # Snapshot -------------------------------------------------------------- #
    try:
        response = client.get(f"/api/v1/runs/{run_id}/snapshots")
        if response.status == 200 and isinstance(response.json(), list):
            snapshots = response.json()
            summary["snapshots"] = len(snapshots)
            if snapshots:
                checks.append(
                    CheckResult("full: snapshot", PASS, f"GET /snapshots -> 200, {len(snapshots)} snapshot(s) created")
                )
            else:
                checks.append(
                    CheckResult(
                        "full: snapshot",
                        FAIL,
                        "GET /snapshots -> 200 but the run has no snapshot",
                        "the finalizer must save an automatic snapshot; check logs/app.log",
                    )
                )
        else:
            checks.append(
                CheckResult(
                    "full: snapshot",
                    FAIL,
                    f"GET /snapshots -> HTTP {response.status} {error_code(response)}".strip(),
                    "check the snapshots endpoint and logs/app.log",
                )
            )
    except DemoAPIError as exc:
        checks.append(CheckResult("full: snapshot", FAIL, str(exc), "check backend health"))

    # Conflicts ------------------------------------------------------------- #
    try:
        response = client.get(f"/api/v1/runs/{run_id}/conflicts")
        if response.status == 200 and isinstance(response.json(), list):
            conflicts = response.json()
            summary["conflicts"] = len(conflicts)
            checks.append(
                CheckResult(
                    "full: conflicts endpoint",
                    PASS,
                    f"GET /conflicts -> 200, {len(conflicts)} resolved conflict(s)",
                )
            )
        else:
            checks.append(
                CheckResult(
                    "full: conflicts endpoint",
                    FAIL,
                    f"GET /conflicts -> HTTP {response.status} {error_code(response)}".strip(),
                    "check the synthesis conflicts endpoint and logs/app.log",
                )
            )
    except DemoAPIError as exc:
        checks.append(CheckResult("full: conflicts endpoint", FAIL, str(exc), "check backend health"))

    _emit(
        args,
        f"Demo run {run_id} finished: status={summary['status']}, tree_nodes={summary['tree_nodes']}, "
        f"snapshots={summary['snapshots']}, conflicts={summary['conflicts']}, "
        f"closing_message={summary['final_message']}, elapsed={format_duration(elapsed)}",
    )
    return summary


# --------------------------------------------------------------------------- #
# Command line
# --------------------------------------------------------------------------- #


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Demo-day preflight for the Activity Descriptor stack (quick health check + optional full pipeline smoke).",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "examples:\n"
            "  python scripts/demo_preflight.py\n"
            "  python scripts/demo_preflight.py --json\n"
            "  python scripts/demo_preflight.py --full\n"
            "  python scripts/demo_preflight.py --full --timeout 1200 --profession \"Data Analyst\"\n"
        ),
    )
    parser.add_argument("--base-url", default=os.environ.get("DEMO_API_URL", DEFAULT_BASE_URL), help=f"backend base URL (default {DEFAULT_BASE_URL})")
    parser.add_argument("--frontend-url", default=DEFAULT_FRONTEND_URL, help=f"frontend URL (default {DEFAULT_FRONTEND_URL})")
    parser.add_argument("--prometheus-url", default=DEFAULT_PROMETHEUS_URL, help=f"Prometheus URL (default {DEFAULT_PROMETHEUS_URL})")
    parser.add_argument("--grafana-url", default=DEFAULT_GRAFANA_URL, help=f"Grafana URL (default {DEFAULT_GRAFANA_URL})")
    parser.add_argument("--jaeger-url", default=DEFAULT_JAEGER_URL, help=f"Jaeger URL (default {DEFAULT_JAEGER_URL})")
    parser.add_argument("--token", help="already issued admin JWT (never printed); default: DEMO_TOKEN/API_TOKEN or mint from JWT_SECRET")
    parser.add_argument("--full", action="store_true", help="also create and drive a full interview -> analysis -> synthesis run via the API")
    parser.add_argument("--force", action="store_true", help="allow --full even when no LLM configuration evidence is visible")
    parser.add_argument("--json", action="store_true", help="print one machine-readable JSON report to stdout")
    parser.add_argument("--timeout", type=float, default=DEFAULT_PIPELINE_TIMEOUT, help=f"overall --full pipeline timeout in seconds (default {DEFAULT_PIPELINE_TIMEOUT:.0f})")
    parser.add_argument("--answer-timeout", type=float, default=DEFAULT_ANSWER_TIMEOUT, help=f"timeout for one interview question in seconds (default {DEFAULT_ANSWER_TIMEOUT:.0f})")
    parser.add_argument("--poll", type=float, default=DEFAULT_POLL_INTERVAL, help=f"polling interval in seconds (default {DEFAULT_POLL_INTERVAL:.0f})")
    parser.add_argument("--http-timeout", type=float, default=DEFAULT_HTTP_TIMEOUT, help=f"single HTTP request timeout in seconds (default {DEFAULT_HTTP_TIMEOUT:.0f})")
    parser.add_argument("--profession", default=DEFAULT_PROFESSION, help=f"profession for the --full run (default {DEFAULT_PROFESSION!r})")
    parser.add_argument("--skip-docker", action="store_true", help="skip the optional docker ps PostgreSQL check")
    return parser


def _validate_args(parser: argparse.ArgumentParser, args) -> None:
    if args.timeout <= 0 or args.answer_timeout <= 0 or args.poll <= 0 or args.http_timeout <= 0:
        parser.error("--timeout, --answer-timeout, --poll and --http-timeout must be positive")
    if not args.profession.strip():
        parser.error("--profession must not be empty")
    if not args.base_url.strip():
        parser.error("--base-url must not be empty")


def render_human(checks: list[CheckResult], mode: str, args, pipeline: dict | None, elapsed_s: float) -> None:
    print(f"Demo preflight ({mode} mode)")
    print(f"API: {args.base_url}")
    print("=" * 78)
    for check in checks:
        print(f"[{check.status}] {check.name}")
        if check.detail:
            print(f"       {check.detail}")
    failures = [check for check in checks if check.status == FAIL]
    warnings = [check for check in checks if check.status == WARN]
    passed = [check for check in checks if check.status == PASS]
    print("-" * 78)
    if failures:
        print("ACTION REQUIRED:")
        for check in failures:
            print(f" - {check.name}: {check.hint or check.detail}")
        print()
    print(f"RESULT: {len(passed)} passed, {len(warnings)} warning(s), {len(failures)} failed")
    if pipeline and pipeline.get("run_id"):
        print(
            f"DEMO RUN (kept as backup): {pipeline['run_id']} | status={pipeline.get('status')} | "
            f"tree_nodes={pipeline.get('tree_nodes')} | snapshots={pipeline.get('snapshots')} | "
            f"closing_message={pipeline.get('final_message')} | elapsed={format_duration(pipeline.get('elapsed_s') or 0)}"
        )
    print(f"total time: {format_duration(elapsed_s)}")
    print(
        "verdict: " + ("READY for the demo" if not failures else "NOT READY - fix the failing checks above")
    )


def main(argv=None) -> int:
    for stream in (sys.stdout, sys.stderr):
        try:
            stream.reconfigure(errors="replace")
        except (AttributeError, OSError):
            pass
    parser = build_parser()
    args = parser.parse_args(argv)
    _validate_args(parser, args)
    started = time.monotonic()
    checks, context = run_quick_checks(args)
    pipeline_summary = None
    if args.full:
        pipeline_summary = run_full_pipeline(args, context, checks)

    failures = [check for check in checks if check.status == FAIL]
    elapsed = time.monotonic() - started
    if args.json:
        report = {
            "ok": not failures,
            "mode": "full" if args.full else "quick",
            "base_url": args.base_url,
            "generated_at": datetime.now(timezone.utc).isoformat(),
            "elapsed_s": round(elapsed, 2),
            "checks": [
                {"name": check.name, "status": check.status, "detail": check.detail, "hint": check.hint}
                for check in checks
            ],
            "summary": {
                "pass": sum(1 for check in checks if check.status == PASS),
                "warn": sum(1 for check in checks if check.status == WARN),
                "fail": len(failures),
            },
            "pipeline": pipeline_summary,
        }
        print(json.dumps(report, ensure_ascii=True, indent=2))
    else:
        render_human(checks, "full" if args.full else "quick", args, pipeline_summary, elapsed)
    return EXIT_OK if not failures else EXIT_FAIL


if __name__ == "__main__":
    sys.exit(main())
