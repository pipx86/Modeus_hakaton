-- name: LockFileImportRequest :exec
SELECT pg_advisory_xact_lock(hashtextextended('file-import:' || sqlc.arg(scope_key)::text, 0));

-- name: GetFileImportResult :one
SELECT body_hash, report
FROM file_import_results
WHERE scope_key=sqlc.arg(scope_key)::text AND expires_at > now();

-- name: SaveFileImportResult :exec
INSERT INTO file_import_results(scope_key, body_hash, report)
VALUES (sqlc.arg(scope_key)::text, sqlc.arg(body_hash)::text, sqlc.arg(report)::jsonb)
ON CONFLICT (scope_key) DO UPDATE
SET body_hash=EXCLUDED.body_hash, report=EXCLUDED.report, expires_at=EXCLUDED.expires_at;

-- name: PruneExpiredFileImportResults :exec
DELETE FROM file_import_results
WHERE scope_key IN (
    SELECT scope_key FROM file_import_results WHERE expires_at <= now()
    ORDER BY expires_at LIMIT 128 FOR UPDATE SKIP LOCKED
);
