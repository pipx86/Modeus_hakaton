-- +goose Up
-- История диалога чат-интервьюера. Раньше чат жил только в памяти процесса,
-- из-за чего после перезапуска backend GET /api/v1/runs/{id}/messages отвечал
-- 404 для уже завершённых исследований. Строки удаляются каскадно с прогоном.
CREATE TABLE run_chat_messages (
    id TEXT PRIMARY KEY CHECK (btrim(id) <> ''),
    run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('user', 'agent')),
    content TEXT NOT NULL CHECK (btrim(content) <> ''),
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    citations JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id, sequence)
);

-- +goose Down
DROP TABLE run_chat_messages;
