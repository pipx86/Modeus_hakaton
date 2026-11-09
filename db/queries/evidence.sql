-- name: CreateEvidence :one
INSERT INTO evidence (id, node_id, source_id, quote, approved) VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: GetEvidence :one
SELECT * FROM evidence WHERE id = $1;

-- name: ListEvidenceByNode :many
SELECT * FROM evidence WHERE node_id = $1 ORDER BY created_at, id;

-- name: UpdateEvidence :one
UPDATE evidence SET source_id = $2, quote = $3, approved = $4 WHERE id = $1 RETURNING *;

-- name: DeleteEvidence :execrows
DELETE FROM evidence WHERE id = $1;
