# Live LLM settings report

Дата: 2026-11-18  
Провайдер: ReformBoss  
Модель: `deepseek-v4.1-flash-1017`  
Prompt contract: `agent.io/v1`

## 1. Полные pipeline по трём профессиям

Все прогоны: 6 ответов, финальный статус `done`, финальное сообщение интервьюера с бесплатными курсами Stepik.

| Профессия | Run ID | Ответов | Узлов дерева | Курсы Stepik | Статус |
|---|---|---:|---:|---|---|
| Data Analyst | `301a3ae8-e020-4e7d-91b5-bbd8dd7637e4` | 6/6 | 12 | SQL + BI | done |
| DevOps Engineer | `18704d85-eee8-4c60-b08c-42b6975bee72` | 6/6 | 8 | DevOps + Docker | done |
| Backend Developer | `bb9eb9c9-4cec-4913-af36-a22888bc3205` | 6/6 | 13 | Go + Django | done |

Проверено:

- вопросы идут через interviewer;
- analyst формирует сущности с evidence;
- synthesizer сохраняет дерево;
- final message приходит в тот же чат;
- курсы соответствуют профессии.

## 2. Температурная матрица interviewer

`max_tokens=1000`, `timeout=120s`.

| Temperature | Success | JSON valid | Duration | Tokens |
|---:|---|---|---:|---:|
| 0.0 | yes | yes | 5.1 s | 2792 |
| 0.5 | yes | yes | 4.1 s | 2674 |
| 1.0 | yes | yes | 4.4 s | 2785 |

Вывод: interviewer стабилен на всём диапазоне 0.0–1.0. Для продукта оставляем умеренную температуру `0.2–0.4`, чтобы сохранить вариативность вопросов без лишнего шума.

## 3. Температурная матрица analyst

### Недостаточный token budget

При `max_tokens=1500` все три температуры вернули пустой ответ (`empty response content`, HTTP 200). Это не ошибка guardrail и не ошибка схемы — модели не хватило лимита на длинный JSON аналитика.

### Рабочий token budget

При `max_tokens=5000`:

| Temperature | Success | JSON valid | Duration | Completion tokens |
|---:|---|---|---:|---:|
| 0.0 | yes | yes | 20.7 s | 3879 |
| 0.5 | yes | yes | 25.2 s | 4829 |
| 1.0 | yes | yes | 12.3 s | 2015 |

Вывод: 5000 — рабочий, но слишком тесный лимит: на temperature 0.5 ответ занял 4829 из 5000 (96.6%). Для production подняли дефолт analyst до **8000** completion-токенов. `max_tokens` — это верхняя граница, а не предоплата: платятся только фактически сгенерированные токены, поэтому запас не увеличивает счёт без необходимости.

## 4. Модели

Подробный bakeoff: [LLM_MODEL_BAKEOFF.md](LLM_MODEL_BAKEOFF.md).

- interviewer: `deepseek-v4.1-flash-1017` — стабильно работает.
- analyst: рекомендуется `minimax-m3.1-flash` — быстрее и дешевле DeepSeek Flash при большем evidence coverage; fallback — `deepseek-v4.1-flash-1017`.
- `deepseek-v4-pro`: медленный (84 с) и нестабильный на analyst; не используем.
- `qwen3.8-flash`: пустой content; `google/gemini-3.6-flash`: 403 у провайдера.

## 5. Рекомендуемые настройки

```env
LLM_MODEL=deepseek-v4.1-flash-1017
LLM_BASE_URL=https://api.reformboss.com/v1
LLM_TIMEOUT_S=300

LLM_INTERVIEWER_MODEL=deepseek-v4.1-flash-1017
LLM_INTERVIEWER_TEMPERATURE=0.2
LLM_INTERVIEWER_MAX_TOKENS=1500
LLM_INTERVIEWER_TIMEOUT_S=300

LLM_ANALYST_MODEL=deepseek-v4.1-flash-1017
LLM_ANALYST_TEMPERATURE=0.2
LLM_ANALYST_MAX_TOKENS=8000
LLM_ANALYST_TIMEOUT_S=300
```

## 6. Проверки после живых прогонов

- `llm_requests` содержит успешные записи interviewer/analyst;
- полный pipeline не уходит в fallback при стабильном ответе провайдера;
- финальные курсы по профессии приходят в чат;
- дерево сохраняется в PostgreSQL;
- OTel/Jaeger принимает спаны `agent.chat`.

## 7. Что не проверено

- длительная нагрузка и rate limit провайдера;
- качество вопросов на золотом наборе (нужен отдельный eval harness);
- поведение при параллельных запусках нескольких интервью;
- model fallback между провайдерами (сейчас один провайдер).
