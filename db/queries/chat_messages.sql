-- name: RunExists :one
SELECT EXISTS (SELECT 1 FROM runs WHERE id = $1);

-- name: InsertRunChatMessage :execrows
-- interview_id наследуется от курсора активного интервью исследования, поэтому
-- каждая реплика автоматически принадлежит тому респонденту, с которым ведётся
-- текущий диалог.
INSERT INTO run_chat_messages (id, run_id, interview_id, role, content, sequence, citations, created_at)
VALUES ($1, $2,
        (SELECT interview_id FROM run_chat_sessions WHERE run_id = $2),
        $3, $4, $5, $6, $7)
ON CONFLICT (run_id, sequence) DO NOTHING;

-- name: ListRunChatMessages :many
SELECT id, run_id, role, content, sequence, citations, created_at
FROM run_chat_messages
WHERE run_id = $1
ORDER BY sequence;
