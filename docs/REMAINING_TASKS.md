# Backend-фичи наставника: статус и фронт-контракт

Срез: 19.11.2026, ветка `feature/roman/ai`.
Роли разделены: **Роман+агенты — backend**, **Миша — full frontend**.

## 1. Итог

Backend-контракты готовы для всех 12 фич. Фронт Миши уже частично реализован (коммит `65504e4`), остальные фичи ждут новых API.

## 1a. Фронтенд-часть, уже сделанная Мишей

Уже реализовано на существующих данных:

- #1 Provenance Score — бейдж «N типов» в дереве;
- #2 «Почему этот узел здесь» — источник/приоритет/объяснение в диалоге узла;
- #4 Snapshot Story — changelog + копирование;
- #5 Red Flags — фильтр «Проблемные» с причинами;
- #6 Evidence → транскрипт — кнопка «В интервью», скролл/подсветка;
- #7 Review Progress + hotkeys — прогресс и клавиши approve/reject;
- #11 Run-to-run — страница `/compare`.

После backend-контрактов ниже фронт может перейти на более точные данные: `story/story_lines`, `insights`, `coverage`, `zun`, `estimate/cost`, `evidence context`.

## 2. Статус всех фич

| # | Фича | Backend | Frontend |
|---:|---|---|---|
| 1 | Provenance Score | готово: `/insights.provenance` | готово (клиентская агрегация; можно перевести на API) |
| 2 | Why this node | готово: `/insights.node_explanations` | готово |
| 3 | Tree Health Score | готово: `/insights.health` | нет |
| 4 | Snapshot Story | готово: `story`, `story_lines` в snapshot diff | готово |
| 5 | Red Flags | готово: `/insights.flags` | готово |
| 6 | Evidence → транскрипт | готово: `/runs/{id}/evidence/{evidence_id}/context` | готово |
| 7 | Review Progress | готово: `/reviews/evidence/progress` | готово |
| 8 | Market Heat Overlay | готово: `/insights.market_heat` | нет |
| 9 | Coverage Matrix | готово: `/runs/{id}/coverage` (+CSV) | нет |
| 10 | Auto-ZUN | готово: suggestions + apply | нет |
| 11 | Run-to-run | готово: `/runs/{id}/compare` | готово (клиентский diff; можно перевести на API) |
| 12 | Cost/Time Estimate | готово: `/runs/estimate`, `/runs/{id}/cost` | нет |

## 3. Backend-контракты для Миши

## 2. Backend-контракты для Миши

### #1/#2/#3/#5/#8 — один insights-запрос

```http
GET /api/v1/runs/{id}/insights
```

Роли: `admin`, `teacher`, `rop`, `guest`.

Ответ:

```json
{
  "run_id": "...",
  "provenance": {
    "nodes": {
      "<node_id>": {
        "source_count": 2,
        "source_types": ["interview", "standard"],
        "evidence_count": 3
      }
    }
  },
  "health": {
    "score": 97,
    "breakdown": {
      "groundedness": 100,
      "completeness": 89,
      "duplicates": 100,
      "orphans": 100
    },
    "weights": {
      "groundedness": 0.4,
      "completeness": 0.3,
      "duplicates": 0.2,
      "orphans": 0.1
    },
    "counts": {},
    "formula": "..."
  },
  "flags": [
    {
      "node_id": "...",
      "codes": ["NO_CHILDREN"],
      "reasons": ["..."]
    }
  ],
  "market_heat": {
    "source": "run.skill_freq | vacancy_sources | none",
    "sample_size": 2,
    "top": [{"skill": "sql", "frequency": 2}],
    "nodes": {
      "<node_id>": {"score": 2, "matches": ["sql"]}
    }
  },
  "coverage": {
    "columns": ["interview", "standard", "vacancy", "document"],
    "rows": [
      {
        "node_id": "...",
        "title": "...",
        "type": "function",
        "cells": {"interview": 2, "standard": 0, "vacancy": 1, "document": 0}
      }
    ]
  },
  "sources": [{"id": "...", "type": "...", "title": "...", "priority": 300}],
  "node_explanations": {
    "<node_id>": {
      "summary": "...",
      "evidence": [
        {"quote": "...", "source_id": "...", "source_type": "interview", "priority": 300}
      ]
    }
  }
}
```

Health: `score = round(0.40*groundedness + 0.30*completeness + 0.20*duplicates + 0.10*orphans)`. `duplicates`/`orphans` — quality (100 = нет проблем); сырые counts — в `health.counts`.

Flags коды:

```text
SUSPICIOUS_TITLE
DUPLICATE_TITLE
SINGLE_WEAK_SOURCE
NO_CHILDREN
NO_EVIDENCE
```

### #9 Coverage Matrix

```http
GET /api/v1/runs/{id}/coverage
GET /api/v1/runs/{id}/coverage?format=csv
```

JSON: `{run_id, columns, rows}`.
CSV:

```text
node_id,title,type,interview,standard,vacancy,document
```

### #4 Snapshot Story

Уже существующий diff теперь дополнительно возвращает:

```json
{
  "story": "Узлов добавлено: 0, удалено: 0, изменено: 1. ...",
  "story_lines": [
    "Узлов добавлено: 0, удалено: 0, изменено: 1.",
    "Изменён узел «Отчёт»: подтверждения."
  ]
}
```

Место: `GET /runs/{id}/snapshots/{head}/diff?against={base}`.

### #11 Run-to-run

```http
GET /api/v1/runs/{id}/compare?against={other_run_id}
```

Роли: `admin`, `teacher`, `rop`, `guest`.

Ответ:

```json
{
  "run_id": "...",
  "against_run_id": "...",
  "profession": "Data Analyst",
  "same_profession": true,
  "match": {"by_id": 0, "by_title": 12, "unmatched": 2},
  "added": [],
  "removed": [],
  "changed": [
    {"id": "...", "before": {}, "after": {}, "fields": ["description"]}
  ],
  "summary": {"added": 0, "removed": 0, "changed": 1},
  "story": "..."
}
```

Матчинг: id → semantic `type + normalized(title) + ancestor path`; UUID независимых прогонов не совпадают, поэтому основной путь — semantic.

### #7 Review Progress

```http
GET /api/v1/reviews/evidence/progress
GET /api/v1/reviews/evidence/progress?run_id={id}
```

Роли: `admin`, `teacher`, `rop`.

```json
{
  "scope": "global | run",
  "run_id": "...",
  "pending": 12,
  "approved": 30,
  "rejected": 2,
  "total": 44
}
```

### #10 Auto-ZUN

```http
GET /api/v1/runs/{id}/zun/suggestions
```

Роли: `admin`, `teacher`, `rop`.
Ответ — массив:

```json
[
  {
    "node_id": "...",
    "node_title": "...",
    "knowledge": ["..."],
    "skills": ["..."],
    "abilities": ["..."],
    "source": "rule",
    "current_zun": null,
    "can_apply": true
  }
]
```

Apply:

```http
POST /api/v1/runs/{id}/zun/suggestions/apply
{
  "node_id": "...",
  "knowledge": ["..."],
  "skills": ["..."],
  "abilities": ["..."]
}
```

Ответ: `{run_id,node_id,node_title,source,applied,can_apply,current_zun}`.
Если ZUN уже заполнен — `409 ZUN_ALREADY_SET`; ручные значения не перезаписываются.

### #6 Evidence → транскрипт

```http
GET /api/v1/runs/{id}/evidence/{evidence_id}/context
```

Роли: все авторизованные.

```json
{
  "run_id": "...",
  "evidence": {
    "id": "...",
    "node_id": "...",
    "node_title": "...",
    "source_id": "...",
    "source_title": "...",
    "ref_id": "answer-2",
    "quote": "...",
    "status": "pending"
  },
  "messages": [
    {"id": "msg-...", "role": "user", "content": "...", "sequence": 4, "created_at": "..."}
  ],
  "match": {
    "message_id": "msg-...",
    "sequence": 4,
    "role": "user",
    "strategy": "ref_id",
    "contains_quote": true,
    "quote": "...",
    "quote_start": 0,
    "quote_end": 20,
    "score": 1
  },
  "note": "..."
}
```

`match` может быть `null`; `quote_start/quote_end` — rune offsets для подсветки.

### #12 Cost/Time Estimate

До старта:

```http
GET /api/v1/runs/estimate?profession=Data%20Analyst&type=chat
```

Роли: `admin`, `teacher`, `rop`.

```json
{
  "profession": "Data Analyst",
  "type": "chat",
  "sample_size": 3,
  "estimated_tokens": 4200,
  "estimated_duration_seconds": 180,
  "estimated_cost_usd": null,
  "confidence": "low | medium | high",
  "method": "profession_history | partial_history | global_history | defaults"
}
```

После прогона:

```http
GET /api/v1/runs/{id}/cost
```

Роли: как `GET /runs/{id}`.

```json
{
  "run_id": "...",
  "profession": "Data Analyst",
  "type": "chat",
  "status": "done",
  "started_at": "...",
  "finished_at": "...",
  "duration_seconds": 142,
  "request_count": 7,
  "prompt_tokens": 1200,
  "completion_tokens": 800,
  "total_tokens": 2000,
  "cost_usd": null,
  "by_model": [],
  "by_agent": [],
  "status_counts": [],
  "attribution": "run_id | time_window | none",
  "attribution_ambiguous": false,
  "estimate": {}
}
```

`cost_usd` может быть `null`, если провайдер не отдал стоимость; это честное «неизвестно».

## 3. Что осталось фронту (Миша)

1. Вкладка/блок Insights: provenance, health, flags, market heat, coverage.
2. Бейдж provenance у узла.
3. Панель «Почему этот узел здесь» на `node_explanations`.
4. Health badge + разбивка.
5. Red Flags фильтр.
6. Snapshot Story: `story`/`story_lines` + copy.
7. Evidence → transcript: `match.message_id`, `sequence`, `quote_start/quote_end`.
8. Review progress + hotkeys.
9. Market heat overlay.
10. Coverage matrix + CSV download.
11. Auto-ZUN suggestions/apply.
12. Run-to-run compare page.
13. Cost estimate before start + actual after.

## 4. Остаток вне фич наставника

- UI конфликтов (`GET /runs/{id}/conflicts`) — фронт.
- Active SSE через `fetch` с Authorization — фронт.
- Docker full backend `cmd/server` — backend/service.
- Реальный корпоративный SMTP + постоянный HTTPS webhook — внешние доступы.
- Production ops: TLS, Alertmanager, HA, retention.
- Batch UI: `type=batch` + transcript.
- HH token refresh.
- Multi-source extraction limits (~2000 символов на источник, +1 LLM call на источник).

## 5. Проверки

- `go test ./...` — зелёный.
- `go vet ./...` — зелёный.
- `python scripts/check_deploy.py` — `deploy config OK`.
- OpenAPI: 36 paths / 45 operations, 0 unresolved refs, 272 examples valid.
- Live API проверены на `d619…`: insights (health 97), coverage JSON/CSV, compare 200, review progress, ZUN, evidence context, estimate, cost.