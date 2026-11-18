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

| Temperature | Success | JSON valid | Duration | Tokens |
|---:|---|---|---:|---:|
| 0.0 | yes | yes | 20.7 s | 6201 |
| 0.5 | yes | yes | 25.2 s | 7151 |
| 1.0 | yes | yes | 12.3 s | 4337 |

Вывод: для analyst нужен бюджет **не меньше 5000 completion-токенов**, иначе модель обрезает JSON. Текущий дефолт проекта уже 5000 — его нельзя уменьшать.

## 4. Модели

- `deepseek-v4.1-flash-1017` — рабочая модель для interviewer и analyst.
- `deepseek-v4-pro` на analyst-входе вернул пустой ответ (HTTP 200, empty content) и в текущей конфигурации не подходит.
- Модель `Auto`/`deepseek-chat` не тестировались в этом прогоне.

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
LLM_ANALYST_MAX_TOKENS=5000
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
