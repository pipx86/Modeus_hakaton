# Метрики агентов

Реестр метрик: `internal/llm/metrics`.

Он собирает:

- количество вызовов по агенту, модели, статусу и коду ошибки;
- количество ошибок по агенту, модели и коду;
- токены на вход и на выход;
- распределение latency;
- количество вызовов по уровню reasoning.

## Метрики

| Метрика | Тип | Лейблы |
|---|---|---|
| `activity_descriptor_agent_calls_total` | counter | `agent`, `model`, `status`, `code` |
| `activity_descriptor_agent_errors_total` | counter | `agent`, `model`, `code` |
| `activity_descriptor_agent_tokens_total` | counter | `agent`, `model`, `direction` |
| `activity_descriptor_agent_reasoning_total` | counter | `agent`, `reasoning` |
| `activity_descriptor_agent_latency_seconds` | histogram | `agent`, `model` |

`direction`: `input` или `output`.
`status`: `ok` или `error`.
`code`: категория из `llm.Code`, например `rate_limit`, `timeout`, `guardrail`.

В лейблах нет `run_id` и `request_id`, чтобы не раздувать кардинальность.

## Как подключается

`agents.Pool` создаёт реестр и передаёт его:

- в OpenAI-клиент через `WithUsageObserver`;
- в runner через `NewRunnerWithMetrics`.

Пример:

```go
registry := pool.Metrics()
var buf bytes.Buffer
_ = registry.WritePrometheus(&buf)
```

`WritePrometheus` отдаёт обычный text exposition format, его можно повесить на
`/metrics` или отдать в Prometheus-скрейпер.

## Примеры PromQL

Средняя latency по агенту:

```promql
rate(activity_descriptor_agent_latency_seconds_sum[5m])
/
rate(activity_descriptor_agent_latency_seconds_count[5m])
```

Токены на выход по агенту:

```promql
sum by (agent) (rate(activity_descriptor_agent_tokens_total{direction="output"}[5m]))
```

Доля ошибок:

```promql
sum by (agent) (rate(activity_descriptor_agent_calls_total{status="error"}[5m]))
/
sum by (agent) (rate(activity_descriptor_agent_calls_total[5m]))
```
