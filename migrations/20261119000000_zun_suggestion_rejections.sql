-- +goose Up
-- +goose StatementBegin
CREATE TABLE zun_suggestion_rejections (
    run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    node_id UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, node_id)
);

CREATE INDEX zun_suggestion_rejections_run_idx
    ON zun_suggestion_rejections (run_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS zun_suggestion_rejections;
-- +goose StatementEnd
