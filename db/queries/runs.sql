-- name: UpdateRunStatus :execrows
UPDATE runs SET status = $2, updated_at = now() WHERE id = $1;
