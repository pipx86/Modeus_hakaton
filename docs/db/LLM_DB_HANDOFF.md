# Требования к БД для LLM-слоя (переслать владельцу БД / коллеге)

Коротко: нужно добавить аддитивные таблицы, чтобы хранить версии промптов,
вызовы модели, ответы, токены/стоимость/латентность, оценки качества и PII-находки
(без самих PII). Существующие таблицы и миграции не меняем.

## Минимум

1. prompt_versions
   - id text PK (например interviewer/v1/<hash>)
   - agent text CHECK IN ('interviewer','analyst','synthesizer')
   - version text
   - prompt_hash char(64)
   - system_text text
   - created_at timestamptz
   - UNIQUE (agent, version, prompt_hash)

2. llm_requests
   - id uuid PK
   - run_id uuid NULL REFERENCES runs(id)
   - trace_id text (run id / interview session id)
   - agent text
   - provider text (openrouter/ollama)
   - model text
   - prompt_version_id text REFERENCES prompt_versions(id)
   - request_hash char(64)
   - params jsonb (temperature, max_tokens, reasoning)
   - status text (ok/error/timeout/degraded)
   - error_code text
   - started_at timestamptz, finished_at timestamptz
   - latency_ms int
   - prompt_tokens int, completion_tokens int
   - cost_usd numeric(12,6)
   - created_at timestamptz
   - индексы: (run_id), (agent, created_at DESC), (model), (status)

3. llm_responses
   - request_id uuid PK REFERENCES llm_requests(id) ON DELETE CASCADE
   - raw_text text (или parsed_json jsonb)
   - validation_status text (valid/invalid/repaired)
   - repair_attempt int
   - finish_reason text
   - received_at timestamptz

4. llm_evaluations (можно позже)
   - id uuid, request_id uuid, evaluator text, criterion text,
     score numeric, details jsonb, created_at timestamptz

5. pii_findings (можно позже)
   - id uuid, request_id uuid, pii_type text, hash text,
     start_offset int, end_offset int, action text, created_at timestamptz
   - сам PII-текст не хранить

## Приватность и retention

- Секреты/API-ключи не хранить никогда.
- Полные промпты/ответы — только после маскировки PII; raw-текст 30 дней,
  агрегаты — 1 год.
- messages можно не хранить целиком: достаточно request_hash + redacted snapshot.

## Что согласовать

- храним ли сырые промпты/ответы или только хэши + redacted;
- срок хранения;
- кто владелец миграций (DB-поток или AI-поток);
- нужны ли additive-миграции evidence.ref_id (#25) и agent_messages.dedup_key
  (это отдельные миграции, существующую схему не ломают).

## Контекст

Три агента (interviewer / analyst / synthesizer) ходят в Nemotron через OpenRouter.
Нужны версия промпта, факт запроса/ответа, токены/стоимость/латентность, ошибки,
оценки качества и PII-находки, связь с runs/сессией через trace_id.

Если ок — AI-поток подготовит SQL-миграции под ваши naming/структуру, без правок
существующих миграций.
