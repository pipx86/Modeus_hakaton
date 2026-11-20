-- Durable chat/interview session snapshots. run_chat_messages stores the dialog
-- history; run_chat_sessions stores the interviewer cursor so a restarted
-- backend can resume an active interview (see internal/chat).
-- name: GetRunChatSession :one
SELECT run_id, schema_version, profession, prompt_version, status, state, version, created_at, updated_at
FROM run_chat_sessions
WHERE run_id = $1;

-- name: InsertRunChatSession :execrows
-- interview_id берётся из активного интервью исследования, поэтому новый диалог
-- сразу привязывается к респонденту, для которого он начат (см.
-- 20261121100000_run_interviews.sql).
INSERT INTO run_chat_sessions (run_id, schema_version, profession, prompt_version, status, state, version, interview_id)
VALUES ($1, $2, $3, $4, $5, $6, 1,
        (SELECT i.id
         FROM run_interviews i
         WHERE i.run_id = $1 AND i.status = 'active'
         ORDER BY i.started_at DESC
         LIMIT 1))
ON CONFLICT (run_id) DO NOTHING;

-- name: UpdateRunChatSession :execrows
UPDATE run_chat_sessions
SET schema_version = $2,
    profession = $3,
    prompt_version = $4,
    status = $5,
    state = $6,
    version = version + 1,
    updated_at = now()
WHERE run_id = $1 AND version = $7;

-- name: ListRunChatSessionsByStatus :many
SELECT s.run_id::text AS run_id
FROM run_chat_sessions s
JOIN runs r ON r.id = s.run_id
WHERE s.status = $1
  AND r.created_at < $2
ORDER BY s.updated_at, s.run_id;

-- name: ListFinalizedRunChatSessionsWithActiveRuns :many
SELECT s.run_id::text AS run_id,
       (COALESCE(s.state ->> 'final_status', ''))::text AS final_status
FROM run_chat_sessions s
JOIN runs r ON r.id = s.run_id
WHERE s.status IN ('completed', 'failed')
  AND r.status IN ('pending', 'running')
  AND s.updated_at < $1
ORDER BY s.updated_at, s.run_id;

-- name: ListInterruptedChatRuns :many
SELECT r.id::text AS run_id
FROM runs r
WHERE r.type = 'chat'
  AND r.status IN ('pending', 'running')
  AND r.created_at < $1
  AND NOT EXISTS (SELECT 1 FROM run_chat_sessions s WHERE s.run_id = r.id)
ORDER BY r.created_at;

-- name: GetRunChatRunStatus :one
SELECT status
FROM runs
WHERE id = $1;

-- name: GetRunHasTree :one
SELECT (root_node_id IS NOT NULL)::boolean AS has_tree
FROM runs
WHERE id = $1;