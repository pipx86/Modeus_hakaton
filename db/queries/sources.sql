-- name: CreateSource :one
INSERT INTO sources (id, run_id, type, title, content, source_url, external_id, raw_payload, processing_status, processing_error, processed_at, provider)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING *;

-- name: GetSource :one
SELECT * FROM sources WHERE id = $1;

-- name: ListSourcesByRun :many
SELECT s.* FROM sources s JOIN run_sources rs ON rs.source_id = s.id WHERE rs.run_id = $1 ORDER BY s.created_at, s.id;

-- name: UpdateSource :one
UPDATE sources SET run_id=$2, type=$3, title=$4, content=$5, source_url=$6, external_id=$7,
raw_payload=$8, processing_status=$9, processing_error=$10, processed_at=$11, provider=$12, updated_at=now()
WHERE id=$1 RETURNING *;

-- name: DeleteSource :execrows
DELETE FROM sources WHERE id = $1;

-- name: UpsertExternalSource :one
INSERT INTO sources (type, title, content, source_url, provider, external_id, raw_payload)
SELECT $1,$2,$3,$4,$5,$6,$7
WHERE $5::text IS NOT NULL AND btrim($5::text) <> ''
  AND $6::text IS NOT NULL AND btrim($6::text) <> ''
ON CONFLICT (provider, type, external_id)
WHERE provider IS NOT NULL AND external_id IS NOT NULL
DO UPDATE SET title=EXCLUDED.title, content=EXCLUDED.content, source_url=EXCLUDED.source_url,
    raw_payload=EXCLUDED.raw_payload, updated_at=now()
RETURNING *;
