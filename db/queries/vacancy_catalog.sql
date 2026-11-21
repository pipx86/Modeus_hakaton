-- name: MergeVacancyMarketDetails :execrows
INSERT INTO vacancy_market_details (source_id, details)
SELECT id, sqlc.arg(details)::jsonb
FROM sources
WHERE type = 'vacancy' AND provider = 'hh' AND external_id = sqlc.arg(external_id)::text
ON CONFLICT (source_id) DO UPDATE
SET details = vacancy_market_details.details || EXCLUDED.details
WHERE vacancy_market_details.details IS DISTINCT FROM vacancy_market_details.details || EXCLUDED.details;

-- name: ListVacancyCatalog :one
WITH filtered AS MATERIALIZED (
    SELECT * FROM vacancy_catalog v
    WHERE (sqlc.arg(search)::text = '' OR strpos(lower(v.name), lower(sqlc.arg(search)::text)) > 0)
      AND (sqlc.arg(region)::text = '' OR lower(v.area) = lower(sqlc.arg(region)::text))
      AND (sqlc.arg(employer)::text = '' OR lower(v.employer_name) = lower(sqlc.arg(employer)::text))
      AND (sqlc.arg(experience)::text = '' OR v.experience = sqlc.arg(experience)::text)
      AND (sqlc.narg(from_date)::timestamptz IS NULL OR v.published_at >= sqlc.narg(from_date)::timestamptz)
      AND (sqlc.narg(until_date)::timestamptz IS NULL OR v.published_at < sqlc.narg(until_date)::timestamptz)
),
page AS (
    SELECT * FROM filtered
    ORDER BY published_at DESC NULLS LAST, id, source_id
    LIMIT sqlc.arg(page_size)::integer OFFSET sqlc.arg(page_offset)::integer
)
SELECT (SELECT count(*) FROM filtered) AS total,
       COALESCE((SELECT jsonb_agg(to_jsonb(page) - 'description' ORDER BY published_at DESC NULLS LAST, id, source_id) FROM page), '[]'::jsonb)::jsonb AS items;

-- name: GetVacancyCatalogEntry :one
SELECT to_jsonb(v)::jsonb AS item
FROM vacancy_catalog v
WHERE v.id = sqlc.arg(external_id)::text;

-- name: GetVacancyCatalogInfo :one
SELECT count(*) AS total,
       count(*) FILTER (WHERE jsonb_array_length(key_skills) > 0 OR jsonb_array_length(parsed_skills) > 0) AS with_skills,
       min(published_at)::timestamptz AS first_published,
       max(published_at)::timestamptz AS last_published
FROM vacancy_catalog;

-- name: GetVacancyCatalogStats :one
WITH experience AS (
    SELECT v.experience AS level, count(*) AS amount
    FROM vacancy_catalog v
    WHERE v.experience <> ''
    GROUP BY v.experience
),
salary AS (
    SELECT salary_currency AS currency, count(*) AS count,
           round(avg(COALESCE(salary_from, salary_to)))::bigint AS average,
           min(COALESCE(salary_from, salary_to))::bigint AS min,
           max(COALESCE(salary_to, salary_from))::bigint AS max
    FROM vacancy_catalog
    WHERE salary_currency <> '' AND (salary_from IS NOT NULL OR salary_to IS NOT NULL)
    GROUP BY salary_currency
)
SELECT (SELECT count(*) FROM vacancy_catalog) AS total,
       ('{"junior":0,"middle":0,"senior":0}'::jsonb || COALESCE((SELECT jsonb_object_agg(level, amount) FROM experience), '{}'::jsonb))::jsonb AS by_experience,
       COALESCE((SELECT jsonb_agg(to_jsonb(salary) ORDER BY currency) FROM salary), '[]'::jsonb)::jsonb AS salary_by_currency;

-- name: GetVacancyCatalogRankings :one
WITH regions AS (
    SELECT area AS name, count(*) AS count FROM vacancy_catalog
    WHERE area <> '' GROUP BY area ORDER BY count DESC, name LIMIT 10
),
employers AS (
    SELECT employer_name AS name, count(*) AS count FROM vacancy_catalog
    WHERE employer_name <> '' GROUP BY employer_name ORDER BY count DESC, name LIMIT 10
), skill_mentions AS (
    SELECT min(skill) AS name, lower(skill) AS skill_key
    FROM vacancy_catalog CROSS JOIN LATERAL jsonb_array_elements_text(key_skills || parsed_skills) AS skill
    GROUP BY source_id, lower(skill)
), skills AS (
    SELECT min(name) AS name, count(*) AS count
    FROM skill_mentions GROUP BY skill_key ORDER BY count DESC, min(name) LIMIT 20
)
SELECT COALESCE((SELECT jsonb_agg(to_jsonb(regions) ORDER BY count DESC, name) FROM regions), '[]'::jsonb)::jsonb AS top_regions,
       COALESCE((SELECT jsonb_agg(to_jsonb(employers) ORDER BY count DESC, name) FROM employers), '[]'::jsonb)::jsonb AS top_employers,
       COALESCE((SELECT jsonb_agg(to_jsonb(skills) ORDER BY count DESC, name) FROM skills), '[]'::jsonb)::jsonb AS top_skills;
