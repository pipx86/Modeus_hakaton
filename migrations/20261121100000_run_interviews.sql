-- +goose Up
-- +goose StatementBegin
-- Интервью с респондентом как отдельная сущность внутри исследования.
--
-- До этой миграции у исследования могла быть ровно одна чат-сессия
-- (run_chat_sessions.run_id был PRIMARY KEY), поэтому второго респондента в
-- одном исследовании физически нельзя было завести. Теперь:
--   * run_interviews        — реестр интервью исследования (кто респондент,
--                             когда началось и чем закончилось);
--   * run_chat_sessions     — по-прежнему одна строка на исследование, но это
--                             курсор АКТИВНОГО интервью со ссылкой на него;
--   * run_chat_messages     — история диалога, привязанная к интервью, поэтому
--                             ответы разных респондентов не смешиваются.
CREATE TABLE run_interviews (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    -- Обезличенное описание респондента: имя/псевдоним, роль, внешняя метка
    -- (например, канал рекрутинга). Персональные контакты здесь не хранятся.
    respondent_name TEXT NOT NULL DEFAULT '',
    respondent_role TEXT NOT NULL DEFAULT '',
    respondent_ref TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'completed', 'failed')),
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX run_interviews_run_idx ON run_interviews (run_id, started_at);

-- У исследования не может быть двух одновременно активных интервью: второй
-- респондент заводится только после завершения предыдущего интервью.
CREATE UNIQUE INDEX run_interviews_one_active_idx
    ON run_interviews (run_id) WHERE status = 'active';

ALTER TABLE run_chat_sessions
    ADD COLUMN interview_id UUID REFERENCES run_interviews(id) ON DELETE SET NULL;
ALTER TABLE run_chat_messages
    ADD COLUMN interview_id UUID REFERENCES run_interviews(id) ON DELETE SET NULL;

-- Бэкфилл: одно интервью на каждое исследование, где уже есть сессия или
-- сообщения. id = run_id делает бэкфилл детерминированным и позволяет
-- привязать существующую историю одним UPDATE.
INSERT INTO run_interviews (id, run_id, status, finished_at, started_at)
SELECT r.id,
       r.id,
       CASE WHEN s.status IN ('completed', 'failed') THEN 'completed' ELSE 'active' END,
       CASE WHEN s.status IN ('completed', 'failed') THEN now() END,
       COALESCE(s.created_at, r.created_at)
FROM runs r
LEFT JOIN run_chat_sessions s ON s.run_id = r.id
WHERE s.run_id IS NOT NULL
   OR EXISTS (SELECT 1 FROM run_chat_messages m WHERE m.run_id = r.id)
ON CONFLICT (id) DO NOTHING;

UPDATE run_chat_sessions s
SET interview_id = s.run_id
WHERE EXISTS (SELECT 1 FROM run_interviews i WHERE i.id = s.run_id);

UPDATE run_chat_messages m
SET interview_id = m.run_id
WHERE EXISTS (SELECT 1 FROM run_interviews i WHERE i.id = m.run_id);

CREATE INDEX run_chat_messages_interview_idx
    ON run_chat_messages (run_id, interview_id, sequence);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS run_chat_messages_interview_idx;
ALTER TABLE run_chat_messages DROP COLUMN IF EXISTS interview_id;
ALTER TABLE run_chat_sessions DROP COLUMN IF EXISTS interview_id;
DROP TABLE IF EXISTS run_interviews;
-- +goose StatementEnd
