-- name: ListAcademicPrograms :one
WITH filtered AS MATERIALIZED (
    SELECT p.id, p.title, p.organization, p.code
    FROM education_programs p
    WHERE sqlc.arg(search)::text = ''
       OR strpos(lower(p.title), lower(sqlc.arg(search)::text)) > 0
       OR strpos(lower(COALESCE(p.code, '')), lower(sqlc.arg(search)::text)) > 0
),
page AS (
    SELECT * FROM filtered ORDER BY title, id
    LIMIT sqlc.arg(page_size)::integer OFFSET sqlc.arg(page_offset)::integer
)
SELECT (SELECT count(*) FROM filtered) AS total,
       COALESCE((SELECT jsonb_agg(to_jsonb(page) ORDER BY title, id) FROM page), '[]'::jsonb)::jsonb AS items;

-- name: GetAcademicProgram :one
SELECT (to_jsonb(p) || jsonb_build_object(
    'source_ids', COALESCE((SELECT jsonb_agg(ps.source_id ORDER BY ps.source_id) FROM program_sources ps WHERE ps.program_id=p.id), '[]'::jsonb),
    'competencies', COALESCE((
        SELECT jsonb_agg(
            to_jsonb(c) || jsonb_build_object(
                'taxonomy_item_ids', COALESCE((SELECT jsonb_agg(l.taxonomy_item_id ORDER BY l.taxonomy_item_id)
                    FROM competency_taxonomy_links l WHERE l.competency_id=c.id), '[]'::jsonb),
                'ksa', COALESCE((SELECT jsonb_agg(
                    to_jsonb(k) || jsonb_build_object('taxonomy_item_ids', COALESCE((
                        SELECT jsonb_agg(l.taxonomy_item_id ORDER BY l.taxonomy_item_id)
                        FROM ksa_taxonomy_links l WHERE l.ksa_id=k.id
                    ), '[]'::jsonb)) ORDER BY k.kind, k.title, k.id)
                    FROM competency_ksa k WHERE k.competency_id=c.id), '[]'::jsonb)
            ) ORDER BY c.code, c.title, c.id
        ) FROM program_competencies c WHERE c.program_id=p.id
    ), '[]'::jsonb)
))::jsonb AS item
FROM education_programs p WHERE p.id = sqlc.arg(program_id)::uuid;

-- name: ListLocalTaxonomies :many
SELECT id, name, version,
       (SELECT count(*) FROM taxonomy_items i WHERE i.taxonomy_id = t.id) AS item_count
FROM taxonomies t
ORDER BY name, version, id
LIMIT sqlc.arg(page_size)::integer OFFSET sqlc.arg(page_offset)::integer;

-- name: GetLocalTaxonomy :one
SELECT (jsonb_build_object('id', t.id, 'name', t.name, 'version', t.version,
    'items', COALESCE((
        SELECT jsonb_agg(jsonb_build_object(
            'id', i.id, 'parent_id', i.parent_id, 'code', i.code, 'title', i.title, 'description', i.description
        ) ORDER BY i.code, i.title, i.id)
        FROM taxonomy_items i WHERE i.taxonomy_id=t.id
    ), '[]'::jsonb)
))::jsonb AS item
FROM taxonomies t WHERE t.id = sqlc.arg(taxonomy_id)::uuid;

-- name: GetAcademicTaxonomyGap :one
WITH competencies AS (
    SELECT c.id, c.code, c.title, EXISTS (
        SELECT 1 FROM competency_taxonomy_links l JOIN taxonomy_items i ON i.id=l.taxonomy_item_id
        WHERE l.competency_id=c.id AND (sqlc.narg(taxonomy_id)::uuid IS NULL OR i.taxonomy_id=sqlc.narg(taxonomy_id)::uuid)
    ) AS matched
    FROM program_competencies c WHERE c.program_id=sqlc.arg(program_id)::uuid
),
ksa AS (
    SELECT k.id, k.kind, k.title, k.competency_id, EXISTS (
        SELECT 1 FROM ksa_taxonomy_links l JOIN taxonomy_items i ON i.id=l.taxonomy_item_id
        WHERE l.ksa_id=k.id AND (sqlc.narg(taxonomy_id)::uuid IS NULL OR i.taxonomy_id=sqlc.narg(taxonomy_id)::uuid)
    ) AS matched
    FROM competency_ksa k JOIN competencies c ON c.id=k.competency_id
),
target AS (
    SELECT i.id, i.code, i.title,
           EXISTS (SELECT 1 FROM competency_taxonomy_links l JOIN competencies c ON c.id=l.competency_id WHERE l.taxonomy_item_id=i.id)
           OR EXISTS (SELECT 1 FROM ksa_taxonomy_links l JOIN ksa k ON k.id=l.ksa_id WHERE l.taxonomy_item_id=i.id)
           AS matched
    FROM taxonomy_items i WHERE i.taxonomy_id=sqlc.narg(taxonomy_id)::uuid
)
SELECT jsonb_build_object(
    'program_id', p.id,
    'taxonomy_id', sqlc.narg(taxonomy_id)::uuid,
    'competencies', (SELECT count(*) FROM competencies),
    'matched_competencies', (SELECT count(*) FROM competencies WHERE matched),
    'unmatched_competencies', COALESCE((
        SELECT jsonb_agg(to_jsonb(c) - 'matched' ORDER BY c.code, c.title, c.id) FROM competencies c WHERE NOT c.matched
    ), '[]'::jsonb),
    'ksa', (SELECT count(*) FROM ksa),
    'matched_ksa', (SELECT count(*) FROM ksa WHERE matched),
    'unmatched_ksa', COALESCE((
        SELECT jsonb_agg(to_jsonb(k) - 'matched' ORDER BY k.kind, k.title, k.id) FROM ksa k WHERE NOT k.matched
    ), '[]'::jsonb),
    'taxonomy_items', (SELECT count(*) FROM target),
    'matched_taxonomy_items', (SELECT count(*) FROM target WHERE matched),
    'uncovered_taxonomy_items', COALESCE((
        SELECT jsonb_agg(to_jsonb(i) - 'matched' ORDER BY i.code, i.title, i.id) FROM target i WHERE NOT i.matched
    ), '[]'::jsonb)
)::jsonb AS report
FROM education_programs p
WHERE p.id=sqlc.arg(program_id)::uuid
  AND (sqlc.narg(taxonomy_id)::uuid IS NULL OR EXISTS(SELECT 1 FROM taxonomies t WHERE t.id=sqlc.narg(taxonomy_id)::uuid));
