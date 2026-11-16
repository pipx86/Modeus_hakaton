-- +goose Up
-- История событий сохраняется вместе с исследованием и удаляется при его удалении.
CREATE TABLE run_sse_events (
    sequence_id BIGSERIAL PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE,
    run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX run_sse_events_run_sequence_idx ON run_sse_events (run_id, sequence_id);

-- +goose Down
DROP TABLE run_sse_events;
