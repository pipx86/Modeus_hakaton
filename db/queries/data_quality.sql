-- name: ListTreeDataIssues :many
WITH RECURSIVE ancestors AS (
    SELECT n.id AS origin_id, n.run_id, n.parent_id,
           ARRAY[n.id]::uuid[] AS visited, false AS has_cycle
    FROM nodes n
    UNION ALL
    SELECT a.origin_id, a.run_id, p.parent_id,
           a.visited || p.id, p.id = ANY(a.visited)
    FROM ancestors a
    JOIN nodes p ON p.id = a.parent_id AND p.run_id = a.run_id
    WHERE NOT a.has_cycle
), issues AS (
    SELECT n.run_id, n.id AS node_id, 'EMPTY_TITLE'::text AS code
    FROM nodes n WHERE n.title ~ '^[[:space:]]*$'
    UNION ALL
    SELECT n.run_id, n.id, 'ORPHAN_NODE'::text
    FROM nodes n JOIN runs r ON r.id = n.run_id
    LEFT JOIN nodes p ON p.id = n.parent_id AND p.run_id = n.run_id
    WHERE (n.parent_id IS NOT NULL AND p.id IS NULL)
       OR (n.parent_id IS NULL AND n.id IS DISTINCT FROM r.root_node_id)
    UNION ALL
    SELECT n.run_id, n.id, 'DUPLICATE_SIBLING_TITLE'::text
    FROM nodes n JOIN (
        SELECT run_id, parent_id, regexp_replace(title, '^[[:space:]]+|[[:space:]]+$', '', 'g') AS normalized_title
        FROM nodes GROUP BY run_id, parent_id, regexp_replace(title, '^[[:space:]]+|[[:space:]]+$', '', 'g') HAVING count(*) > 1
    ) duplicates ON duplicates.run_id = n.run_id
        AND duplicates.parent_id IS NOT DISTINCT FROM n.parent_id
        AND duplicates.normalized_title = regexp_replace(n.title, '^[[:space:]]+|[[:space:]]+$', '', 'g')
    UNION ALL
    SELECT n.run_id, n.id, 'MISSING_EVIDENCE'::text
    FROM nodes n WHERE NOT EXISTS (SELECT 1 FROM evidence e WHERE e.node_id = n.id)
    UNION ALL
    SELECT DISTINCT a.run_id, a.origin_id, 'CYCLE_DETECTED'::text
    FROM ancestors a WHERE a.has_cycle
)
SELECT run_id, node_id, code FROM issues ORDER BY run_id, node_id, code;
