# LLM observability schema

Migration: `20261115000000_llm_observability.sql`.

## Tables

- `prompt_versions` — версии системных промптов; один active на агента;
- `prompt_activations` — аудит активаций версий;
- `llm_requests` — запрос к модели: trace/run, агент, провайдер, модель, версия промпта, статус, latency;
- `llm_responses` — ответ модели: raw/parsed, validation status, repair attempt;
- `llm_usage` — токены на вход/выход, стоимость, reasoning effort, latency;
- `llm_evaluations` — оценки качества;
- `pii_findings` — типы PII, хэши и позиции без открытого текста;
- `agent_messages.dedup_key` — ключ дедупликации исходящих событий.

## Замечания

- Схема аддитивная: существующие таблицы и данные не меняются.
- `llm_requests.run_id` может быть NULL для системных вызовов.
- `prompt_version_id` может быть NULL для старых/внешних вызовов.
- `llm_responses.raw_text` и `llm_requests.messages` могут содержать чувствительные данные; retention и маскирование должен обеспечивать слой приложения.
- `pii_findings` не хранит исходный PII-текст: только тип, хэш и позиции.
