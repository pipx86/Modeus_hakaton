-- +goose Up
-- +goose StatementBegin
-- Durable snapshot of the active chat/interview session. run_chat_messages keeps
-- the dialog history; this table keeps the interviewer cursor (plan, questions,
-- answers, fallback counter, clarification cursor) so a restarted backend can
-- continue the interview instead of leaving the run hanging. Rows are deleted
-- cascading with the run.
CREATE TABLE run_chat_sessions (
    run_id UUID PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    profession TEXT NOT NULL CHECK (btrim(profession) <> ''),
    prompt_version TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL
        CHECK (status IN ('active', 'finalizing', 'completed', 'failed')),
    state JSONB NOT NULL CHECK (jsonb_typeof(state) = 'object'),
    -- Optimistic concurrency token: every saved step increments it, so two
    -- concurrent writers cannot advance the same interview twice.
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX run_chat_sessions_status_idx
    ON run_chat_sessions (status, updated_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS run_chat_sessions;
-- +goose StatementEnd
