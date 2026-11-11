# Grafana dashboard (monitoring profile)

Grafana is provisioned from files — no click-ops setup:

| File | Purpose |
| ---- | ------- |
| `deploy/grafana/provisioning/datasources/prometheus.yml` | Prometheus datasource, `uid: prometheus`, `url: http://prometheus:9090` |
| `deploy/grafana/provisioning/dashboards/dashboards.yml` | Loads every JSON from `/var/lib/grafana/dashboards` every 30s |
| `deploy/grafana/dashboards/activity-descriptor.json` | Dashboard `activity-descriptor` (Go backend) |
| `deploy/prometheus/prometheus.yml` | Scrapes `api:8080/metrics` every 15s |

Compose mounts both directories read-only into the `grafana` service under the
`monitoring` profile.

## Run it

```bash
cd .worktrees/devops
cp .env.example .env
docker compose --profile monitoring up -d --build api prometheus grafana

# generate traffic for the HTTP panels
for i in $(seq 1 20); do curl -fsS http://localhost:8080/health >/dev/null; done
```

* Grafana: <http://localhost:3000> — user `admin` (or `GRAFANA_ADMIN_USER`),
  password from `GRAFANA_ADMIN_PASSWORD` (default `admin_dev_password` in
  `.env.example`). Anonymous viewing is enabled for local demos.
* Prometheus: <http://localhost:9090> — `Status → Targets`, the
  `activity-descriptor-api` target must be `UP`.
* Dashboard: **Dashboards → Activity Descriptor → Activity Descriptor — Go
  backend**.

Without Docker: the dashboard/JSON can still be validated with
`make check-deploy`; the demo API can be run locally to produce the same
`/metrics` payload.

## Panels

| Panel | PromQL |
| ----- | ------ |
| API up | `up{job="activity-descriptor-api"}` |
| Requests / sec | `sum(rate(activity_descriptor_http_requests_total[5m]))` |
| 5xx error ratio (5m) | `sum(rate(..._http_requests_total{status=~"5.."}[5m])) / clamp_min(sum(rate(..._http_requests_total[5m])), 1e-6)` |
| In-flight requests | `activity_descriptor_http_in_flight_requests` |
| Request rate by route | `sum(rate(activity_descriptor_http_requests_total[5m])) by (route)` |
| HTTP latency p95 by route | `histogram_quantile(0.95, sum(rate(..._http_request_duration_seconds_bucket[5m])) by (le, route))` |
| Responses by status code | `sum(rate(activity_descriptor_http_requests_total[5m])) by (status)` |
| Latency p50 / p95 / p99 | `histogram_quantile(0.5/0.95/0.99, sum(rate(..._http_request_duration_seconds_bucket[5m])) by (le))` |
| Interview chat HTTP traffic | `sum(rate(..._http_requests_total{route="/api/v1/runs/{id}/messages"}[5m])) by (method)` |
| 5xx rate by route | `sum(rate(..._http_requests_total{status=~"5.."}[5m])) by (route)` |

Metric names come from `internal/metrics`; run identifiers are never labels
(`docs/METRICS.md`). The chat panel uses the normalized route template
`/api/v1/runs/{id}/messages`, so it does not depend on concrete run ids.

## Verify provisioning without the UI

```bash
docker compose exec grafana ls /etc/grafana/provisioning/datasources /etc/grafana/provisioning/dashboards
docker compose exec grafana ls /var/lib/grafana/dashboards
curl -s -u admin:admin_dev_password http://localhost:3000/api/datasources | head -c 400
curl -s -u admin:admin_dev_password "http://localhost:3000/api/search?query=Activity" | head -c 400
```

CI validates the dashboard JSON, every queried metric name against
`internal/metrics`, and that no panel queries a run identifier. `docker compose
config` runs in CI for the default and profile service sets.

## Screenshot

A screenshot was **not captured**: the Docker daemon is not reachable from the
authoring session, so there was no live Grafana. To capture one later:

```bash
docker compose --profile monitoring up -d api prometheus grafana
# open http://localhost:3000/d/activity-descriptor and save a PNG to docs/img/
```

## Troubleshooting

* **Panels empty** — generate traffic first; a fresh API only exposes a metric
  family after its first sample. Check `curl localhost:8080/metrics`.
* **Datasource not found** — the provisioning path in compose must be
  `deploy/grafana/provisioning` (relative to the compose file).
* **Manual edits disappear** — file-provisioned dashboards are overwritten;
  edit `deploy/grafana/dashboards/activity-descriptor.json` instead.
* **API up = 0** — Prometheus cannot reach `api:8080`; check the api
  healthcheck and `docker compose logs prometheus`.
