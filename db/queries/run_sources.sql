-- name: LinkSourceToRun :execrows
INSERT INTO run_sources (run_id, source_id) VALUES ($1, $2) ON CONFLICT DO NOTHING;
