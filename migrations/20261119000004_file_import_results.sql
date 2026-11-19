-- +goose Up
CREATE TABLE file_import_results (
    scope_key TEXT PRIMARY KEY CHECK (length(scope_key) = 64),
    body_hash TEXT NOT NULL CHECK (length(body_hash) = 64),
    report JSONB NOT NULL CHECK (jsonb_typeof(report) = 'object'),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '1 hour'
);
CREATE INDEX idx_file_import_results_expires_at ON file_import_results(expires_at);

-- +goose Down
DROP TABLE file_import_results;
