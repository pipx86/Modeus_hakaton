-- name: LockDemoFixture :exec
SELECT pg_advisory_xact_lock(732014091);

-- name: CreateDemoRun :execrows
INSERT INTO runs (id, profession) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING;

-- name: GetDemoRun :one
SELECT id, profession, root_node_id FROM runs WHERE id = $1;
