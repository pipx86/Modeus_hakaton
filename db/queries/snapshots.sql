-- name: LockRunForSnapshot :one
SELECT id, root_node_id FROM runs WHERE id = $1 FOR UPDATE;

-- name: BuildSnapshotTree :one
SELECT jsonb_build_object(
    'schema_version', 1,
    'root_node_id', sqlc.arg(root_node_id)::uuid,
    'nodes', COALESCE((
        SELECT jsonb_agg(jsonb_build_object(
            'id', n.id, 'parent_id', n.parent_id, 'level', n.level,
            'type', n.type, 'title', n.title, 'description', n.description,
            'evidence', COALESCE((
                SELECT jsonb_agg(jsonb_build_object(
                    'id', e.id, 'ref_id', e.ref_id, 'quote', e.quote, 'approved', e.approved,
                    'source', jsonb_build_object(
                        'id', s.id, 'type', s.type, 'title', s.title, 'source_url', s.source_url
                    )
                ) ORDER BY e.created_at, e.id)
                FROM evidence e JOIN sources s ON s.id = e.source_id WHERE e.node_id = n.id
            ), '[]'::jsonb)
        ) ORDER BY n.level, n.created_at, n.id)
        FROM nodes n WHERE n.run_id = sqlc.arg(run_id)::uuid
    ), '[]'::jsonb)
)::jsonb AS tree;

-- name: CreateSnapshot :one
INSERT INTO snapshots (run_id, version, tree)
VALUES (sqlc.arg(run_id), (SELECT COALESCE(MAX(version), 0) + 1 FROM snapshots WHERE run_id = sqlc.arg(run_id)), sqlc.arg(tree))
RETURNING *;

-- name: GetSnapshot :one
SELECT * FROM snapshots WHERE run_id = $1 AND id = $2;

-- name: ListSnapshotsByRun :many
SELECT * FROM snapshots WHERE run_id = $1 ORDER BY version;

-- name: SnapshotRootExists :one
SELECT EXISTS(SELECT 1 FROM nodes WHERE id = $1 AND run_id = $2 AND parent_id IS NULL AND type = 'role');
