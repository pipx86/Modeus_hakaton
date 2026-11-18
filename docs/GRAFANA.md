# Grafana + Prometheus (monitoring profile)

Grafana, Prometheus and Jaeger run under the `monitoring` compose profile and are
provisioned entirely from files — no click-ops setup. This document also records
the hardening applied for the demo and the exact checks run against the live
stack (last verified 2026-11-18, checks finishing after midnight on 2026-11-19; host backend on `http://127.0.0.1:8080`).

What changed versus the earlier local demo:

* Grafana **anonymous access is disabled**; every dashboard/API request must
  authenticate. The admin user and password come from
  `GRAFANA_ADMIN_USER` / `GRAFANA_ADMIN_PASSWORD`, never from code.
* Prometheus and Grafana are published on **127.0.0.1 only** (same for Jaeger
  and, in the compose file, PostgreSQL). Prometheus and Jaeger have no
  authentication of their own; host-only binding is the local-demo mitigation.
  Production requirements are in [Production caveats](#production-caveats).
* The two scrape modes use **separate Prometheus configs**, selected with
  `PROMETHEUS_CONFIG`. The host-backend demo never contains a dead `api:8080`
  target, and the compose mode never contains a dead host target.
* The business dashboard now shows **real HTTP/SLO data** and an **explicit
  "no LLM traffic" state** instead of an empty screen, plus alert rules backed
  by [`deploy/prometheus/alerts.yml`](../deploy/prometheus/alerts.yml).

| File | Purpose |
| ---- | ------- |
| [`deploy/prometheus/prometheus.yml`](../deploy/prometheus/prometheus.yml) | Default scrape config: compose `api:8080` + Prometheus self-scrape + alert rules |
| [`deploy/prometheus/prometheus.host.yml`](../deploy/prometheus/prometheus.host.yml) | Host-demo scrape config: `host.docker.internal:8080` only + self-scrape + alert rules |
| [`deploy/prometheus/alerts.yml`](../deploy/prometheus/alerts.yml) | SLO alerting rules (API down, 5xx, p95 latency, LLM errors, run failures) |
| [`deploy/grafana/provisioning/datasources/prometheus.yml`](../deploy/grafana/provisioning/datasources/prometheus.yml) | Prometheus datasource, `uid: prometheus`, `url: http://prometheus:9090`, read-only |
| [`deploy/grafana/provisioning/dashboards/dashboards.yml`](../deploy/grafana/provisioning/dashboards/dashboards.yml) | Loads every JSON from `/var/lib/grafana/dashboards` every 30 s; UI edits and deletion disabled |
| [`deploy/grafana/dashboards/activity-descriptor.json`](../deploy/grafana/dashboards/activity-descriptor.json) | Dashboard `activity-descriptor` — HTTP traffic, latency, errors (10 panels) |
| [`deploy/grafana/dashboards/activity-descriptor-business.json`](../deploy/grafana/dashboards/activity-descriptor-business.json) | Dashboard `activity-descriptor-business` — live HTTP/SLO data + LLM/agent/run panels (14 panels) |
| [`docker-compose.yml`](../docker-compose.yml) | `prometheus`, `grafana`, `jaeger` services under the `monitoring` profile |

Metric families are defined in `internal/metrics` and
[`docs/METRICS.md`](METRICS.md); the agent/LLM families are documented in
[`docs/AGENT_METRICS.md`](AGENT_METRICS.md). Traces are covered in
[`docs/OBSERVABILITY.md`](OBSERVABILITY.md) — the same `monitoring` profile also
runs `jaeger` on <http://localhost:16686>.

## Security model

| Control | Where | Value / behaviour |
| ------- | ----- | ----------------- |
| Anonymous access | `docker-compose.yml` (`GF_AUTH_ANONYMOUS_ENABLED`) | `false` — a browser request to `/d/...` gets `302 -> /login`; the HTTP API returns `401` |
| Admin credentials | `docker-compose.yml` (`GF_SECURITY_ADMIN_USER` / `GF_SECURITY_ADMIN_PASSWORD`) | taken from the environment; the value in the compose file is a local placeholder only |
| Sign-up / analytics | `docker-compose.yml` | `GF_USERS_ALLOW_SIGN_UP=false`, analytics/reporting and update checks off |
| Network exposure | `docker-compose.yml` ports | `127.0.0.1:3000` (Grafana), `127.0.0.1:9090` (Prometheus), `127.0.0.1:16686` + `127.0.0.1:4318` (Jaeger) |
| Dashboard drift | `deploy/grafana/provisioning/dashboards/dashboards.yml` | `allowUiUpdates: false`, `disableDeletion: true` — the JSON files in git are the only source of truth |
| Datasource | `deploy/grafana/provisioning/datasources/prometheus.yml` | `editable: false`, server-side proxy (no Browser access) |

### Setting the admin password

The password is environment-based. For anything beyond a throwaway local
checkout, set a unique value (for example in the git-ignored `.env`) before
starting the stack:

```bash
export GRAFANA_ADMIN_PASSWORD="$(openssl rand -base64 24)"   # or put it in .env
PROMETHEUS_CONFIG=prometheus.host.yml \
  docker compose --profile monitoring up -d --no-deps prometheus grafana jaeger
```

Grafana applies `GF_SECURITY_ADMIN_PASSWORD` **when the admin user is first
created**. If the `grafana_data` volume already contains an admin user (a
previously started stack), reset the password explicitly so the env value and
the database agree:

```bash
docker compose --profile monitoring exec -T grafana \
  grafana cli admin reset-admin-password "$GRAFANA_ADMIN_PASSWORD"
```

(On Windows PowerShell pass `$env:GRAFANA_ADMIN_PASSWORD` as the argument of the last line.)

## Scrape modes — two configs, no expected-DOWN target

`docker-compose.yml` mounts all of `deploy/prometheus` at `/etc/prometheus` and
loads `/etc/prometheus/${PROMETHEUS_CONFIG:-prometheus.yml}`. Only one config is
active, so the Prometheus UI never lists a target that cannot be scraped in the
current mode.

| `PROMETHEUS_CONFIG` | Jobs | UP when |
| ------------------- | ---- | ------- |
| `prometheus.yml` (default) | `activity-descriptor-api` → `api:8080` (`deployment="compose"`), `prometheus` | the compose `api` service runs (it is a compose `depends_on` of `prometheus`) |
| `prometheus.host.yml` | `activity-descriptor-api-host` → `host.docker.internal:8080` (`deployment="host"`), `prometheus` | a backend runs on the Docker host (`make run`, `go run ./cmd/server`) |

Both configs load the same alert rules. `host.docker.internal` is added with
`extra_hosts: host.docker.internal:host-gateway`, so the host target resolves on
Linux Docker Engine too (Docker Desktop provides the name natively). On Linux
the host backend must listen on `0.0.0.0:8080`, not only `127.0.0.1`.

Switching modes is just a restart with a different value (no file edits):

```bash
# host backend (local demo)
PROMETHEUS_CONFIG=prometheus.host.yml \
  docker compose --profile monitoring up -d --no-deps --force-recreate prometheus

# back to the compose api mode (also starts the api service through depends_on)
# stop the host backend first, or set API_PORT=18080 while it still owns :8080
docker compose --profile monitoring up -d --build api prometheus
```

After a mode switch, the removed target's last sample can remain visible in
range queries for up to Prometheus' 5-minute lookback (the series is not in
`/api/v1/targets`). `Targets` is authoritative; the dashboard target panel uses
`last_over_time(...[1m])` and the API-down alert uses `for: 6m` so this
transition does not create a false DOWN row or a false page.

### Run it

```bash
# Option A — backend on the host (current local/demo setup).
# --no-deps matters: otherwise the prometheus depends_on starts the compose
# `api` container, which cannot bind :8080 while the host backend owns it.
PROMETHEUS_CONFIG=prometheus.host.yml \
  docker compose --profile monitoring up -d --no-deps prometheus grafana jaeger

# Option B — API inside compose (default prometheus.yml).
docker compose --profile monitoring up -d --build api prometheus grafana jaeger
```

URLs and credentials (all ports are host-only):

| Service | URL | Credentials |
| ------- | --- | ----------- |
| Grafana | <http://localhost:3000> | `admin` / value of `GRAFANA_ADMIN_PASSWORD` (compose fallback is a local placeholder); anonymous access is disabled |
| HTTP dashboard | <http://localhost:3000/d/activity-descriptor> | Grafana login required |
| Business dashboard | <http://localhost:3000/d/activity-descriptor-business> | Grafana login required |
| Prometheus | <http://localhost:9090> | none (localhost only) |
| Jaeger (tracing) | <http://localhost:16686> | none (localhost only) |

## Verify — commands and results from the live stack

All commands below were executed on 2026-11-18 against the running stack
(`activity-descriptor-prometheus-1`, `activity-descriptor-grafana-1`,
`activity-descriptor-jaeger-1`; host backend on `127.0.0.1:8080`), with
`PROMETHEUS_CONFIG=prometheus.host.yml` and a freshly generated
`GRAFANA_ADMIN_PASSWORD`. The host backend was not restarted or modified.

### Static validation (CI gate)

```bash
docker compose --profile monitoring config --quiet                       # exit 0
docker compose --profile monitoring --profile automation config --quiet   # exit 0
python scripts/check_deploy.py                                           # "deploy config OK"
docker compose --profile monitoring run --rm --no-deps --entrypoint promtool prometheus \
  check config /etc/prometheus/prometheus.yml
docker compose --profile monitoring run --rm --no-deps --entrypoint promtool prometheus \
  check config /etc/prometheus/prometheus.host.yml
docker compose --profile monitoring run --rm --no-deps --entrypoint promtool prometheus \
  check rules /etc/prometheus/alerts.yml
```

Observed: compose config `exit 0` for the default and monitoring+automation
profiles; `check_deploy.py` printed `deploy config OK`; promtool printed
`SUCCESS: 1 rule files found`, `SUCCESS: /etc/prometheus/prometheus.yml is valid
prometheus config file syntax`, `SUCCESS: /etc/prometheus/prometheus.host.yml is
valid prometheus config file syntax`, `SUCCESS: 5 rules found`.

### Prometheus targets — no misleading DOWN api:8080

```bash
curl -fsS http://localhost:9090/-/healthy     # Prometheus Server is Healthy.
curl -s http://localhost:9090/api/v1/targets | python -m json.tool
curl -sG http://localhost:9090/api/v1/query --data-urlencode 'query=up'
curl -s http://localhost:9090/api/v1/status/config   # loaded scrape config
```

Observed with `prometheus.host.yml` (the chosen demo run):

| Job | Scrape URL | Health | Labels |
| --- | ---------- | ------ | ------ |
| `activity-descriptor-api-host` | `http://host.docker.internal:8080/metrics` | **up** | `deployment="host"` |
| `prometheus` | `http://localhost:9090/metrics` | **up** | — |

`droppedTargets: 0`; `up` returned exactly two series, both `1`; the loaded
config contains only the host target (no `api:8080`).

The compose-mode config was also verified **live**, without touching the host
backend: the compose `api` was started on a free host port and a throwaway
Prometheus container used the default config on the compose network:

```bash
API_PORT=18080 docker compose --profile monitoring up -d --no-deps api
docker run -d --name ops-prom-compose-test --network activity-descriptor_default \
  -p 127.0.0.1:19090:9090 \
  -v "$PWD/deploy/prometheus:/etc/prometheus:ro" prom/prometheus:v2.54.1 \
  --config.file=/etc/prometheus/prometheus.yml --storage.tsdb.path=/tmp/prom-data
# then: GET http://localhost:19090/api/v1/targets and /api/v1/query?query=up
```

Observed in compose mode: `activity-descriptor-api` → `http://api:8080/metrics`
**up** (`deployment="compose"`), `prometheus` **up**, `droppedTargets: 0`,
`up{job="activity-descriptor-api"} = 1`, demo API metrics scraped, all 5 alert
rules loaded and inactive. The temporary Prometheus and compose API containers
were removed afterwards; the running monitoring stack stayed in host mode.

### Alert rules

```bash
curl -s http://localhost:9090/api/v1/rules | python -m json.tool
curl -s http://localhost:9090/api/v1/alerts
```

Observed: group `activity-descriptor-slo` with 5 rules, all `health: ok` and
`state: inactive`; `/api/v1/alerts` returned no active alerts. The rules are:

| Alert | Fires when |
| ----- | ---------- |
| `ActivityDescriptorApiDown` | the API scrape target reports `up == 0` for 6 minutes |
| `ActivityDescriptorHigh5xxRate` | 5xx share > 5% over 5 minutes (rule needs traffic; SLO target < 1%) |
| `ActivityDescriptorHighLatencyP95` | HTTP p95 > 2 s for 10 minutes |
| `ActivityDescriptorLlmErrorRatioHigh` | agent error ratio > 5% over 10 minutes |
| `ActivityDescriptorRunFailures` | > 25% of runs in the last 30 minutes failed/degraded |

Prometheus evaluates and displays them, but this demo has **no Alertmanager**, so
nothing is routed to a receiver (documented production gap).

### Grafana authentication

```bash
curl -s -o /dev/null -w '%{http_code} %{redirect_url}\n' http://localhost:3000/d/activity-descriptor
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:3000/api/search
curl -s -o /dev/null -w '%{http_code}\n' -u "admin:$GRAFANA_ADMIN_PASSWORD" http://localhost:3000/api/user
curl -s -o /dev/null -w '%{http_code}\n' -u "admin:admin_dev_password" http://localhost:3000/api/user
```

Observed:

| Request | Result |
| ------- | ------ |
| `GET /d/activity-descriptor` (no credentials) | `302` → `http://127.0.0.1:3000/login` |
| `GET /api/search` (no credentials) | `401` |
| `GET /api/dashboards/uid/activity-descriptor` (no credentials) | `401` |
| `GET /api/user` with the env password | `200`, login `admin`, Grafana admin |
| `GET /api/user` with the old fallback password | `401` |

So anonymous access is off and the account is driven by the env password (the
old placeholder stopped working after the reset).

### Grafana provisioning, datasource and dashboards

```bash
curl -fsS http://localhost:3000/api/health
curl -fsS -u "admin:$GRAFANA_ADMIN_PASSWORD" http://localhost:3000/api/datasources/uid/prometheus/health
curl -fsS -u "admin:$GRAFANA_ADMIN_PASSWORD" 'http://localhost:3000/api/search?query=Activity'
curl -fsS -u "admin:$GRAFANA_ADMIN_PASSWORD" http://localhost:3000/api/dashboards/uid/activity-descriptor
curl -fsS -u "admin:$GRAFANA_ADMIN_PASSWORD" http://localhost:3000/api/dashboards/uid/activity-descriptor-business
docker compose --profile monitoring logs grafana | grep -i 'provision'
```

Observed: `/api/health` → `{"database":"ok","version":"11.2.0"}`; datasource
health → `{"status":"OK","message":"Successfully queried the Prometheus API."}`;
search returns the `Activity Descriptor` folder and both dashboards (HTTP: 10
panels, business: 14 panels, both `provisioned: true`); Grafana logs show
`starting to provision dashboards` / `finished to provision dashboards` and no
provisioning errors. Grafana 11.2.0 also logs a benign, image-internal
`plugin xychart is already registered` message that is present in an unmodified
container as well; it does not affect provisioning or querying.

### Business dashboard — real data and explicit no-traffic state

Panel expressions were run through Grafana's own query API
(`POST /api/ds/query`, i.e. the same path the panels use) while the host backend
was idle for LLM traffic:

| Panel | Expression | Observed |
| ----- | ---------- | -------- |
| HTTP requests (selected range) | `sum(increase(activity_descriptor_http_requests_total[$__range]))` | real data: ~499 requests in the last hour |
| API availability | `avg_over_time((max(up{job=~"activity-descriptor-api.*"}))[$__range:1m])` | `1` (100% of scrapes up) |
| Scrape targets (this config) | `last_over_time(up{job=~"activity-descriptor-api.*\|prometheus"}[1m])` | 2 series, both `1` (host API + Prometheus; no `api:8080`) |
| HTTP latency p95 by route | `histogram_quantile(0.95, sum(rate(..._http_request_duration_seconds_bucket[$__rate_interval])) by (le, route))` | real per-route p95 values (e.g. 0.005–0.095 s) |
| LLM calls (last 15m) | `sum(increase(activity_descriptor_agent_calls_total[15m])) or vector(0)` | returns `0` when there is no recent traffic; the value mapping renders **"no LLM traffic (15m)"** |
| LLM error ratio (last 5m) | error ratio over `[5m]` | no samples → panel shows its no-data state (the stat above states the no-traffic condition explicitly; a 0/0 ratio is deliberately not shown as 0%) |

The agent panels are not faked: the backend process running since 23:02 had
served no LLM-backed interview at verification time, so the live-traffic stats
show the explicit no-data message, and the dashboard description/text panel
explains how to populate them. Prometheus still had **real** agent samples from
the previous backend process (last scrape 23:02), which proves the business
metric wiring end to end:

```bash
curl -sG http://localhost:9090/api/v1/query \
  --data-urlencode 'query=activity_descriptor_agent_calls_total' | python -m json.tool
# earlier (retained) samples, for example:
#   {agent="interviewer", model="deepseek-v4.1-flash-1017", ...} = 6
#   {agent="analyst",     model="minimax-m3.1-flash",      ...} = 5
#   {agent="analyst",     model="deepseek-v4.1-flash-1017", ...} = 4
```

To see live agent data, run one LLM-backed interview against the running
backend (see [`docs/API.md`](API.md) and [`docs/DEMO_RUNBOOK.md`](DEMO_RUNBOOK.md));
the agent panels populate within one scrape interval (15 s). Counters reset when
the backend process restarts — Prometheus keeps the history for its retention
window. The live-traffic stats are deliberately scoped to the last 15/5 minutes
so old samples cannot masquerade as current traffic.

### Jaeger

```bash
curl -fsS http://localhost:16686/                    # UI 200
curl -fsS http://localhost:16686/api/services        # {"data":[...],"total":...,"errors":null}
```

Observed: Jaeger UI `200` and `/api/services` `200`. Jaeger all-in-one stores
traces in memory, so the container recreate used for the hardening cleared the
previously recorded `activity-descriptor` traces; during the final verification
pass new real spans arrived from an instrumented backend — by the end of the
pass `/api/services` contained `jaeger-all-in-one` + `activity-descriptor` and
`/api/traces?service=activity-descriptor&limit=50` returned **12 real traces**
(00:03–00:07, 7 `interviewer` spans and 5 `analyst` spans, models
`deepseek-v4.1-flash-1017` / `minimax-m3.1-flash`, `otel.status_code=OK`), so the
exporter → collector → UI pipeline is live. Recreating the Jaeger container
clears its in-memory history — a demo-storage property, not a tracing
regression. See [`docs/OBSERVABILITY.md`](OBSERVABILITY.md) for the detail and the
production tracing caveats.

### Host-only port binding

```bash
# Linux
ss -ltnp | grep -E ':(3000|9090|16686|4318)\b'
# Windows PowerShell
Get-NetTCPConnection -State Listen | Where-Object { $_.LocalPort -in 3000,9090,16686,4318 }
```

Observed: `127.0.0.1:3000` (Grafana), `127.0.0.1:9090` (Prometheus),
`127.0.0.1:16686` and `127.0.0.1:4318` (Jaeger). No `0.0.0.0`/`[::]` listener
for these ports.

> PostgreSQL is also pinned to `127.0.0.1` in `docker-compose.yml`, but the
> database container that was already running before this change still shows
> `0.0.0.0:5432` until it is next recreated. It was deliberately not restarted
> here because the host backend uses it.

## Panels

### HTTP dashboard (`activity-descriptor.json`, 10 panels)

| Panel | PromQL |
| ----- | ------ |
| API up | `max(last_over_time(up{job=~"activity-descriptor-api.*"}[1m]))` |
| Requests / sec | `sum(rate(activity_descriptor_http_requests_total[5m]))` |
| 5xx error ratio (5m) | `sum(rate(..._http_requests_total{status=~"5.."}[5m])) / clamp_min(sum(rate(..._http_requests_total[5m])), 1e-6)` |
| In-flight requests | `activity_descriptor_http_in_flight_requests` |
| Request rate by route | `sum(rate(activity_descriptor_http_requests_total[5m])) by (route)` |
| HTTP latency p95 by route | `histogram_quantile(0.95, sum(rate(..._http_request_duration_seconds_bucket[5m])) by (le, route))` |
| Responses by status code | `sum(rate(activity_descriptor_http_requests_total[5m])) by (status)` |
| Latency p50 / p95 / p99 | `histogram_quantile(0.5/0.95/0.99, sum(rate(..._http_request_duration_seconds_bucket[5m])) by (le))` |
| Interview chat HTTP traffic | `sum(rate(..._http_requests_total{route="/api/v1/runs/{id}/messages"}[5m])) by (method)` |
| 5xx rate by route | `sum(rate(..._http_requests_total{status=~"5.."}[5m])) by (route)` |

Stat panels show explicit `noValue` messages (`API not scraped`, `no traffic in
window`, `no requests yet`) instead of a blank/"No data" state.

### Business dashboard (`activity-descriptor-business.json`, 14 panels)

| Panel | Query / purpose |
| ----- | --------------- |
| API scrape | `max(last_over_time(up{job=~"activity-descriptor-api.*"}[1m]))` — UP/DOWN mapping |
| HTTP requests (selected range) | real request count for the dashboard time range |
| 5xx error ratio (SLO < 1%) | real 5xx share, green/orange/red thresholds |
| API availability (SLO >= 99.9%) | share of successful scrapes over the range |
| LLM calls (last 15m) | live agent traffic; explicit no-data message when idle |
| LLM error ratio (last 5m) | live agent error share; explicit no-data message when idle |
| HTTP request rate by route | real traffic by normalized route |
| HTTP latency p95 by route | real per-route p95 |
| LLM requests by agent | rate of agent calls by `agent` |
| LLM tokens by direction | input/output token rate by `agent` |
| LLM latency p95 | `histogram_quantile(0.95, ...agent_latency_seconds_bucket...)` by agent |
| Runs by status | completed runs per 10 minutes by `status` |
| Prometheus scrape targets (this config) | table of the configured target(s) with UP/DOWN mapping |
| How to read this dashboard | markdown explaining real data vs. the no-traffic state and the SLO targets |

Agent/LLM families come from `internal/llm/metrics` and are described in
[`docs/AGENT_METRICS.md`](AGENT_METRICS.md). Run identifiers are never labels
(`docs/METRICS.md`), so the chat panel uses the normalized route template
`/api/v1/runs/{id}/messages`.

## Production caveats

This stack is a **local demo**, not a production deployment. Before exposing it
beyond localhost:

1. **TLS everywhere.** Grafana and Prometheus listen on plain HTTP in this
   compose file. Terminate TLS in a reverse proxy (Caddy, nginx, Traefik) or run
   Grafana with `GF_SERVER_PROTOCOL=https` + certificate files and
   `GF_SERVER_ROOT_URL`; enable secure cookies (`GF_SECURITY_COOKIE_SECURE=true`)
   and HSTS at the proxy.
2. **Prometheus authentication.** Prometheus has no built-in auth. Either expose
   it only on an internal network/behind a proxy with basic auth (`--web.config.file`
   with `basic_auth_users` and TLS) or use a managed Prometheus. Never publish
   `9090` publicly.
3. **Jaeger is unauthenticated and all-in-one.** Its in-memory store is not
   durable and the UI must not be public. Production: OTLP collector with
   TLS/auth, persistent backend (Tempo/Jaeger with Elasticsearch/Cassandra),
   sampling and retention policies.
4. **Credential management.** Do not use the compose fallback or the
   `.env.example` placeholder outside a sandbox: put `GRAFANA_ADMIN_PASSWORD`
   (and any other secret) in a secret manager / injected environment, rotate it,
   and note that changing `GF_SECURITY_ADMIN_PASSWORD` requires the
   `grafana cli admin reset-admin-password` step on an existing volume.
5. **Retention and backups.** Prometheus keeps data on the
   `prometheus_data` volume (default retention 15 days); configure
   `--storage.tsdb.retention.time` / `--storage.tsdb.retention.size`, monitor
   disk, and back up the volume if the history matters. Grafana state (users,
   folders, annotations; dashboards are re-provisioned from files) lives in
   `grafana_data` (SQLite) — for real use switch to an external database and
   back it up.
6. **High availability.** One Prometheus and one Grafana is a single point of
   failure. Production needs HA Prometheus (replicas + remote write to
   Thanos/Mimir/VictoriaMetrics or a managed service), an external Grafana DB,
   and at least two replicas behind a load balancer.
7. **Alert routing.** `deploy/prometheus/alerts.yml` evaluates rules but there is
   no Alertmanager in this profile, so nothing pages anyone. Production: add
   Alertmanager (HA pair, TLS, auth) with receivers/on-call routing, and consider
   SLO burn-rate multi-window rules.
8. **Cardinality/limits.** Keep the no-run-id label rule; review
   `activity_descriptor_*` label cardinality before scaling scrape volume.
9. **Known app-side gap (not fixed here — outside the deploy scope).**
   `internal/metrics` registers `activity_descriptor_vacancies_loaded` but its
   `WriteTo` family list omits the `vacancies` gauge, so the metric is never
   exposed. The dashboards therefore do not query it; a fresh interview run
   (`runs_total`) and the agent families cover the business view. Fix belongs in
   `internal/metrics/metrics.go` (add `m.vacancies` to the exported families).
10. **Benign image message.** Grafana 11.2.0 logs `xychart is already registered`
    at startup; it is image-internal (also present without this repo's
    provisioning) and does not affect functionality.

## Troubleshooting

* **Login loop / admin password rejected** — Grafana only applies
  `GF_SECURITY_ADMIN_PASSWORD` on first admin creation. Reset with
  `docker compose --profile monitoring exec -T grafana grafana cli admin reset-admin-password "<new>"`;
  verify with `curl -u admin:"<new>" http://localhost:3000/api/user` (`200`).
* **Anonymous access still works** — an old container is still running without
  the new env: `docker compose --profile monitoring up -d --no-deps --force-recreate grafana`
  and check `docker compose --profile monitoring config | grep GF_AUTH`.
* **Expected DOWN target after switching modes** — the removed target's last
  sample lingers in range queries for ≤5 minutes (not in `/api/v1/targets`, and
  the alert has `for: 6m`). No action needed; do not add a second scrape job.
* **`activity-descriptor-api` UP = 0 in compose mode** — check
  `docker compose ps api` and `docker compose logs prometheus`; Prometheus
  `depends_on` the api healthcheck, so it should not run before the API is
  healthy.
* **`host.docker.internal` does not resolve** — the compose file already adds
  `extra_hosts: host.docker.internal:host-gateway`; on Linux the backend must
  listen on `0.0.0.0:8080` (not only `127.0.0.1`) for the container to reach it.
* **Port 8080 already allocated in Option A** — use `--no-deps`
  (`docker compose --profile monitoring up -d --no-deps prometheus grafana jaeger`),
  or `API_PORT=18080` when also running the compose API.
* **Business agent panels empty** — the backend process has not served an
  LLM-backed interview since it started. Check the process log for
  `LLM provider configured`; run an interview; wait one scrape interval. Empty
  is shown explicitly, never faked.
* **Dashboard edits disappear** — dashboards are file-provisioned and UI edits
  are disabled; edit the JSON in `deploy/grafana/dashboards/` and wait ≤30 s
  (or restart Grafana).
* **Datasource not found** — the compose volume must mount
  `deploy/grafana/provisioning` to `/etc/grafana/provisioning`; check
  `docker compose --profile monitoring exec -T grafana ls /etc/grafana/provisioning/datasources`.
* **Provisioning "no such file or directory" errors** — the `alerting/` and
  `plugins/` provisioning placeholders keep those directories present; do not
  delete them without updating the mount.
* **Secret scan in CI** — `scripts/check_deploy.py` scans this directory for
  obvious secrets; never commit a real `.env`, Grafana password or provider key.
