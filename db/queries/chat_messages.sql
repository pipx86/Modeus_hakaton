-- name: RunExists :one
SELECT EXISTS (SELECT 1 FROM runs WHERE id = $1);

-- name: InsertRunChatMessage :execrows
INSERT INTO run_chat_messages (id, run_id, role, content, sequence, citations, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (run_id, sequence) DO NOTHING;

-- name: ListRunChatMessages :many
SELECT id, run_id, role, content, sequence, citations, created_at
FROM run_chat_messages
WHERE run_id = $1
ORDER BY sequence;
