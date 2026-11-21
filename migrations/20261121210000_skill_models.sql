-- +goose Up
CREATE TABLE skill_model_revisions (
 run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK (revision > 0),
 payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
 created_by TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY (run_id, revision)
);

-- +goose Down
DROP TABLE skill_model_revisions;
