-- name: CreateSource :one
INSERT INTO sources (id, run_id, type, title, content, source_url, external_id, raw_payload, processing_status, processing_error, processed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING *;

-- name: GetSource :one
SELECT * FROM sources WHERE id = $1;

-- name: ListSourcesByRun :many
SELECT * FROM sources WHERE run_id = $1 ORDER BY created_at, id;

-- name: UpdateSource :one
UPDATE sources SET run_id=$2, type=$3, title=$4, content=$5, source_url=$6, external_id=$7,
raw_payload=$8, processing_status=$9, processing_error=$10, processed_at=$11, updated_at=now()
WHERE id=$1 RETURNING *;

-- name: DeleteSource :execrows
DELETE FROM sources WHERE id = $1;
