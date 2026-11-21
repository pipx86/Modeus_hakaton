-- +goose Up
CREATE TABLE run_review_access (
    run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, user_id)
);

-- +goose Down
DROP TABLE IF EXISTS run_review_access;
