-- name: CreateNode :one
INSERT INTO nodes (id, run_id, parent_id, level, type, title, description)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: GetNode :one
SELECT * FROM nodes WHERE id = $1;

-- name: ListNodesByRun :many
SELECT * FROM nodes WHERE run_id = $1 ORDER BY level, created_at, id;

-- name: UpdateNode :one
UPDATE nodes SET parent_id = $2, level = $3, type = $4, title = $5, description = $6, updated_at = now()
WHERE id = $1 RETURNING *;

-- name: DeleteNode :execrows
DELETE FROM nodes WHERE id = $1;

-- name: SetRunRoot :execrows
UPDATE runs SET root_node_id = $2, updated_at = now() WHERE id = $1 AND root_node_id IS NULL;
