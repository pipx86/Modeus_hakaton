# Grafana + Prometheus (monitoring profile)

Grafana and Prometheus run under the `monitoring` compose profile and are
provisioned entirely from files — no click-ops setup. The stack is verified
against the current local/demo API: a backend started on the Docker host
(`http://127.0.0.1:8080`) is scraped through `host.docker.internal:8080`, while
the normal compose `api:8080` target stays configured for the all-in-compose
mode.

| File | Purpose |
| ---- | ------- |
| [`deploy/prometheus/prometheus.yml`](../deploy/prometheus/prometheus.yml) | Scrape configs: compose `api:8080`, host backend `host.docker.internal:8080`, Prometheus self-scrape |
| [`deploy/grafana/provisioning/datasources/prometheus.yml`](../deploy/grafana/provisioning/datasources/prometheus.yml) | Prometheus datasource, `uid: prometheus`, `url: http://prometheus:9090` |
| [`deploy/grafana/provisioning/dashboards/dashboards.yml`](../deploy/grafana/provisioning/dashboards/dashboards.yml) | Loads every JSON from `/var/lib/grafana/dashboards` every 30s |
| [`deploy/grafana/dashboards/activity-descriptor.json`](../deploy/grafana/dashboards/activity-descriptor.json) | Dashboard `activity-descriptor` (HTTP traffic, latency, errors) |
| [`deploy/grafana/dashboards/activity-descriptor-business.json`](../deploy/grafana/dashboards/activity-descriptor-business.json) | Dashboard `activity-descriptor-business` (LLM/agent, runs, vacancies) |
| [`docker-compose.yml`](../docker-compose.yml) | `prometheus` + `grafana` services under the `monitoring` profile (plus `jaeger`) |

Metric families are defined in `internal/metrics` and
[`docs/METRICS.md`](METRICS.md); the agent/LLM families are documented in
[`docs/AGENT_METRICS.md`](AGENT_METRICS.md). Traces are covered in
[`docs/OBSERVABILITY.md`](OBSERVABILITY.md) — the same `monitoring` profile also
runs `jaeger` on <http://localhost:16686>.

## Scrape targets

There are two API scrape jobs because the backend can run in two places.
Normally **exactly one of them is UP**; the other is DOWN and that is expected.

| Job | Target | UP when |
| --- | ------ | ------- |
| `activity-descriptor-api` | `api:8080` (`deployment="compose"`) | the compose `api` service is running |
| `activity-descriptor-api-host` | `host.docker.internal:8080` (`deployment="host"`) | a backend is started on the Docker host (`make run`, `go run ./cmd/server`) and Docker Desktop forwards `host.docker.internal` to it |
| `prometheus` | `localhost:9090` | always (self-scrape) |

The host target is what makes the **current local/demo setup** observable:
the backend is bound to `127.0.0.1:8080` on the host, so the Prometheus
container cannot use `api:8080` (that DNS name only exists while the compose
`api` container runs) but it can reach the host through
`host.docker.internal`, which Docker Desktop forwards to the host loopback.

To point at another host/port, edit the `activity-descriptor-api-host` target
in [`deploy/prometheus/prometheus.yml`](../deploy/prometheus/prometheus.yml)
and reload Prometheus — no container restart is needed:

```bash
curl -fsS -X POST http://localhost:9090/-/reload   # 200 while --web.enable-lifecycle is on
```

**Docker Desktop vs Linux Docker Engine.** Docker Desktop (Windows/macOS)
resolves `host.docker.internal` natively — verified on Windows with Docker
29.9.2 / Compose v5.6.1. On Linux Docker Engine add the name to the Prometheus
service through an override file, and note that a backend bound to
`127.0.0.1` is only reachable through Docker Desktop's host forwarding; on
Linux the backend must listen on `0.0.0.0:8080` for host scraping:

```yaml
# docker-compose.override.yml (Linux only)
services:
  prometheus:
    extra_hosts:
      - "host.docker.internal:host-gateway"
```

If host scraping is not wanted at all, delete/comment the
`activity-descriptor-api-host` job; the compose `api:8080` job is untouched by
that.

## Run it

### Option A — backend on the host (current local/demo setup)

Start only Prometheus and Grafana. `--no-deps` matters: otherwise the
`prometheus` service's `depends_on: api: service_healthy` starts the compose
`api` container, which cannot bind host port 8080 while the local backend owns
it.

```bash
# 1. backend already serving http://127.0.0.1:8080 (make run / go run ./cmd/server)
# 2. start the monitoring stack without the compose api service
docker compose --profile monitoring up -d --no-deps prometheus grafana
```

### Option B — API inside compose

```bash
docker compose --profile monitoring up -d --build api prometheus grafana
```

Then the `activity-descriptor-api` job scrapes `api:8080` and the host job is
DOWN.

### URLs and credentials

| Service | URL | Credentials |
| ------- | --- | ----------- |
| Grafana | <http://localhost:3000> | `admin` / `admin_dev_password` by default (`GRAFANA_ADMIN_USER` / `GRAFANA_ADMIN_PASSWORD` override); anonymous Viewer access is enabled for local demos |
| Prometheus | <http://localhost:9090> | none |
| Jaeger (tracing) | <http://localhost:16686> | none |
| Go backend dashboard | <http://localhost:3000/d/activity-descriptor> | anonymous / admin |
| Business dashboard | <http://localhost:3000/d/activity-descriptor-business> | anonymous / admin |

## Verify (commands and results from this session)

The following was executed on 2026-11-18 against the running local setup:
host backend on `127.0.0.1:8080`, `jaeger` container up, Prometheus and Grafana
started with Option A.

### Prometheus health and targets

```bash
curl -fsS http://localhost:9090/-/healthy   # Prometheus Server is Healthy.
curl -fsS http://localhost:9090/-/ready     # Prometheus Server is Ready.
docker compose exec -T prometheus promtool check config /etc/prometheus/prometheus.yml
curl -s http://localhost:9090/api/v1/targets | python -m json.tool
```

Observed targets (`Status → Targets`):

| Job | Scrape URL | Health | Note |
| --- | ---------- | ------ | ---- |
| `activity-descriptor-api-host` | `http://host.docker.internal:8080/metrics` | **up** | scraping the local backend |
| `activity-descriptor-api` | `http://api:8080/metrics` | down | expected — compose `api` container is not running in Option A; error is `dial tcp: lookup api ... no such host` |
| `prometheus` | `http://localhost:9090/metrics` | up | self-scrape |

The normal compose target was proven intact separately: starting the compose
API on a free host port makes that job UP and Prometheus scrapes it.

```bash
API_PORT=18080 docker compose --profile monitoring up -d --no-deps api
# after the healthcheck and one scrape interval:
#   activity-descriptor-api  http://api:8080/metrics  up
#   activity_descriptor_http_requests_total{job="activity-descriptor-api",...} => 3
docker compose stop api   # restore the state where the host backend is the only API
```

`promtool check config` result: `SUCCESS: /etc/prometheus/prometheus.yml is
valid prometheus config file syntax`.

### Prometheus queries

```bash
curl -sG http://localhost:9090/api/v1/query --data-urlencode 'query=up'
curl -sG http://localhost:9090/api/v1/query \
  --data-urlencode 'query=activity_descriptor_http_requests_total'
curl -sG http://localhost:9090/api/v1/query \
  --data-urlencode 'query=activity_descriptor_agent_calls_total'
```

Observed:

* `up` → 3 series; `up{job="activity-descriptor-api-host"} = 1`,
  `up{job="activity-descriptor-api"} = 0`, `up{job="prometheus"} = 1`.
* `activity_descriptor_http_requests_total` → 7 series scraped from
  `host.docker.internal:8080`, for example
  `GET /metrics 200 = 14`, `GET /api/v1/health 200 = 7`,
  `POST /unmatched 201 = 2` (the exposition is live and grows with traffic).
* `activity_descriptor_agent_calls_total` → `200`, `status: "success"`,
  `resultType: "vector"`, **empty result**. The running backend process
  (`cmd/server`) has not handled an LLM-backed agent call since its last
  start, so the counter has no samples yet. The family **is** exported on
  `GET /metrics` (`# TYPE activity_descriptor_agent_calls_total counter`) and
  the business dashboard is already wired to it; samples appear after the
  first instrumented interview (`docs/AGENT_METRICS.md`). This is an API
  data-state fact, not a monitoring-stack failure.

### Grafana health, provisioning and dashboard load

```bash
curl -fsS http://localhost:3000/api/health
curl -fsS -u admin:admin_dev_password http://localhost:3000/api/datasources
curl -fsS -u admin:admin_dev_password \
  http://localhost:3000/api/datasources/uid/prometheus/health
curl -fsS -u admin:admin_dev_password \
  'http://localhost:3000/api/search?query=Activity'
curl -fsS -u admin:admin_dev_password \
  http://localhost:3000/api/dashboards/uid/activity-descriptor
curl -fsS -u admin:admin_dev_password \
  http://localhost:3000/api/dashboards/uid/activity-descriptor-business
docker compose exec -T grafana \
  ls /etc/grafana/provisioning/datasources /etc/grafana/provisioning/dashboards \
     /var/lib/grafana/dashboards
```

Observed:

* `/api/health` → `{"database":"ok","version":"11.2.0",...}`.
* `/api/datasources` → one default Prometheus datasource, `uid: prometheus`,
  `url: http://prometheus:9090`, `readOnly: true`.
* `/api/datasources/uid/prometheus/health` → `{"status":"OK","message":
  "Successfully queried the Prometheus API."}` — Grafana reaches Prometheus
  through the compose network.
* `/api/search?query=Activity` → folder **Activity Descriptor** plus both
  dashboards; `/api/dashboards/uid/activity-descriptor` returns 10 panels,
  `/api/dashboards/uid/activity-descriptor-business` returns 8 panels. Both
  load from the provisioned JSON (the `API up` panel expression is
  `max(up{job=~"activity-descriptor-api.*"})`, i.e. the file content, not a
  stale manual copy).
* Grafana logs contain `"finished to provision dashboards"`; the container has
  the provisioning files mounted read-only at `/etc/grafana/provisioning` and
  the dashboard directory at `/var/lib/grafana/dashboards`.
* The dashboard page loads anonymously: `curl -s -o /dev/null -w '%{http_code}'
  http://localhost:3000/d/activity-descriptor` → `200`.
* Grafana → Prometheus proxy query works:
  `GET /api/datasources/proxy/uid/prometheus/api/v1/query?query=up` returns
  `status: "success"` with the same 3 `up` series.

### CI / static validation

```bash
docker compose --profile monitoring config --quiet          # exit 0
docker compose --profile monitoring --profile automation config --quiet  # exit 0
python scripts/check_deploy.py                             # deploy config OK
```

`scripts/check_deploy.py` validates the compose file for both profiles, the
Prometheus/Grafana YAML, the dashboard JSON (10 panels, queried metric families
cross-checked against `internal/metrics`), and the relative links in this
document. It passed in this session.

## Panels

### HTTP dashboard (`activity-descriptor.json`)

| Panel | PromQL |
| ----- | ------ |
| API up | `max(up{job=~"activity-descriptor-api.*"})` |
| Requests / sec | `sum(rate(activity_descriptor_http_requests_total[5m]))` |
| 5xx error ratio (5m) | `sum(rate(..._http_requests_total{status=~"5.."}[5m])) / clamp_min(sum(rate(..._http_requests_total[5m])), 1e-6)` |
| In-flight requests | `activity_descriptor_http_in_flight_requests` |
| Request rate by route | `sum(rate(activity_descriptor_http_requests_total[5m])) by (route)` |
| HTTP latency p95 by route | `histogram_quantile(0.95, sum(rate(..._http_request_duration_seconds_bucket[5m])) by (le, route))` |
| Responses by status code | `sum(rate(activity_descriptor_http_requests_total[5m])) by (status)` |
| Latency p50 / p95 / p99 | `histogram_quantile(0.5/0.95/0.99, sum(rate(..._http_request_duration_seconds_bucket[5m])) by (le))` |
| Interview chat HTTP traffic | `sum(rate(..._http_requests_total{route="/api/v1/runs/{id}/messages"}[5m])) by (method)` |
| 5xx rate by route | `sum(rate(..._http_requests_total{status=~"5.."}[5m])) by (route)` |

### Business dashboard (`activity-descriptor-business.json`)

| Panel | PromQL |
| ----- | ------ |
| API up | `max(up{job=~"activity-descriptor-api.*"})` |
| LLM calls (1h) | `sum(increase(activity_descriptor_agent_calls_total[1h]))` |
| LLM requests by agent | `sum by (agent) (rate(activity_descriptor_agent_calls_total[5m]))` |
| LLM tokens by direction | `sum by (agent,direction) (rate(activity_descriptor_agent_tokens_total[5m]))` |
| LLM latency p95 | `histogram_quantile(0.95, sum(rate(activity_descriptor_agent_latency_seconds_bucket[5m])) by (le,agent))` |
| Run failures (1h) | `sum(increase(activity_descriptor_runs_total{status=~"failed\|degraded"}[1h]))` |
| Runs by status | `sum by (status) (increase(activity_descriptor_runs_total[1h]))` |
| Vacancies loaded | `activity_descriptor_vacancies_loaded` |

The agent/LLM families come from `internal/llm/metrics` and are described in
[`docs/AGENT_METRICS.md`](AGENT_METRICS.md). Run identifiers are never labels
(`docs/METRICS.md`); the chat panel uses the normalized route template
`/api/v1/runs/{id}/messages`, so it does not depend on concrete run ids.

## Screenshot

No screenshot is checked in: the stack was verified through the Prometheus and
Grafana HTTP APIs from this session, which had no browser automation. To
capture one later, open <http://localhost:3000/d/activity-descriptor> (anonymous
Viewer works) and save a PNG.

## Troubleshooting

* **Panels empty** — generate traffic first (`curl -s http://localhost:8080/health`,
  or use the API); a fresh API only exposes an HTTP metric family after its
  first sample. The agent/LLM panels stay empty until an LLM-backed interview
  runs (see `docs/AGENT_METRICS.md`).
* **API up = 0 / `activity-descriptor-api` DOWN** — check which mode is in use:
  compose `api` running means the `api:8080` job should be UP; otherwise the
  host job needs a backend on `127.0.0.1:8080` **and** working
  `host.docker.internal` resolution. `docker compose logs prometheus` prints
  the scrape error for the DOWN target.
* **`host.docker.internal` does not resolve** — Linux Docker Engine needs the
  `extra_hosts` override shown above; Docker Desktop resolves it out of the
  box.
* **Port already allocated on 8080 when using Option A** — the compose `api`
  service was started by `depends_on`; use
  `docker compose --profile monitoring up -d --no-deps prometheus grafana`, or
  set `API_PORT=18080` if you also want the compose API.
* **Datasource not found** — the provisioning path in compose must be
  `deploy/grafana/provisioning` (relative to the compose file). Check
  `docker compose exec -T grafana ls /etc/grafana/provisioning/datasources`.
* **Manual edits disappear** — dashboards are file-provisioned; edit
  [`deploy/grafana/dashboards/activity-descriptor.json`](../deploy/grafana/dashboards/activity-descriptor.json)
  (or the business JSON) and Grafana reloads the file within 30 s.
* **Grafana login** — default password is `admin_dev_password`; override with
  `GRAFANA_ADMIN_USER` / `GRAFANA_ADMIN_PASSWORD` in `.env`. Anonymous Viewer
  access is enabled for local demos.
