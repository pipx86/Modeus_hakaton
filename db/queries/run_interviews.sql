-- Реестр интервью одного исследования: несколько респондентов внутри одного
-- run. Активное интервью ровно одно (частичный unique-индекс в миграции
-- 20261121100000_run_interviews.sql), история диалога ссылается на интервью.

-- name: CreateRunInterview :one
INSERT INTO run_interviews (run_id, respondent_name, respondent_role, respondent_ref)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListRunInterviews :many
SELECT *
FROM run_interviews
WHERE run_id = $1
ORDER BY started_at, id;

-- name: GetRunInterview :one
SELECT *
FROM run_interviews
WHERE id = $1 AND run_id = $2;

-- name: CloseActiveRunInterviews :execrows
UPDATE run_interviews
SET status = 'completed',
    finished_at = now(),
    updated_at = now()
WHERE run_id = $1 AND status = 'active';

-- name: FailActiveRunInterviews :execrows
UPDATE run_interviews
SET status = 'failed',
    finished_at = now(),
    updated_at = now()
WHERE run_id = $1 AND status = 'active';

-- name: DeleteRunChatSession :execrows
DELETE FROM run_chat_sessions WHERE run_id = $1;

-- История ответов конкретного интервью: используется, чтобы показать диалог
-- выбранного респондента, не смешивая его с остальными.
-- name: ListRunChatMessagesByInterview :many
SELECT id, run_id, interview_id, role, content, sequence, citations, created_at
FROM run_chat_messages
WHERE run_id = $1 AND interview_id = $2
ORDER BY sequence;
