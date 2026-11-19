-- +goose Up
-- +goose StatementBegin
ALTER TABLE runs DROP CONSTRAINT IF EXISTS runs_type_check;
ALTER TABLE runs ADD CONSTRAINT runs_type_check CHECK (type IN ('batch', 'chat', 'learning'));

CREATE TABLE IF NOT EXISTS run_learning_plans (
    run_id UUID PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
    plan JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS run_learning_plans;
ALTER TABLE runs DROP CONSTRAINT IF EXISTS runs_type_check;
ALTER TABLE runs ADD CONSTRAINT runs_type_check CHECK (type IN ('batch', 'chat'));
-- +goose StatementEnd
