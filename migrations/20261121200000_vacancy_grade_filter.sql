-- +goose Up
-- Keep source records intact; exclude ungraded vacancies from the analysis and
-- market views so they remain available for future updates and review.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION derive_vacancy_grade(experience_id text, vacancy_title text)
RETURNS text
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
AS $function$
    WITH source_values AS (
        SELECT lower(regexp_replace(COALESCE(experience_id, ''), '[[:space:]]+', '', 'g')) AS experience,
               lower(COALESCE(vacancy_title, '')) AS title
    ), grade_markers AS (
        SELECT experience,
               title ~ $junior$\m(junior|jr|младш[а-яё]*|стаж[её]р[а-яё]*|internship|intern|trainee)\M$junior$ AS junior,
               title ~ $middle$\m(middle|mid|средн[а-яё]*)\M$middle$ AS middle,
               title ~ $senior$\m(senior|старш[а-яё]*|ведущ[а-яё]*)\M$senior$ AS senior
        FROM source_values
    )
    SELECT CASE
        WHEN experience IN ('noexperience', 'no_experience', 'less1', 'less', 'junior') THEN 'junior'
        WHEN experience IN ('between1and3', 'between3and6', 'middle') THEN 'middle'
        WHEN experience IN ('morethan6', 'morethan10', 'more', 'senior') THEN 'senior'
        WHEN junior AND NOT middle AND NOT senior THEN 'junior'
        WHEN middle AND NOT junior AND NOT senior THEN 'middle'
        WHEN senior AND NOT junior AND NOT middle THEN 'senior'
        ELSE ''
    END
    FROM grade_markers;
$function$;
-- +goose StatementEnd

CREATE OR REPLACE VIEW vacancy_catalog AS
SELECT s.id AS source_id, s.external_id AS id, s.title AS name,
       COALESCE(s.content, '') AS description, COALESCE(s.source_url, '') AS alternate_url,
       s.vacancy_published_at AS published_at,
       COALESCE(d.details->>'area', '') AS area,
       COALESCE(d.details->>'employer_name', '') AS employer_name,
       COALESCE(d.details->>'employer_id', '') AS employer_id,
       COALESCE(d.details->>'employer_logo', '') AS employer_logo,
       COALESCE(d.details->'parsed_skills', '[]'::jsonb) AS parsed_skills,
       COALESCE(d.details->>'experience', '') AS experience_id,
       derive_vacancy_grade(COALESCE(d.details->>'experience', ''), s.title) AS experience,
       (d.details #>> '{salary,from}')::bigint AS salary_from,
       (d.details #>> '{salary,to}')::bigint AS salary_to,
       COALESCE(d.details #>> '{salary,currency}', '') AS salary_currency,
       COALESCE(d.details->'key_skills', '[]'::jsonb) AS key_skills,
       COALESCE(d.details->'snippet', '{}'::jsonb) AS snippet
FROM sources s
LEFT JOIN vacancy_market_details d ON d.source_id = s.id
WHERE s.type = 'vacancy' AND s.provider = 'hh' AND s.external_id IS NOT NULL
  AND derive_vacancy_grade(COALESCE(d.details->>'experience', ''), s.title) <> '';

-- +goose Down
CREATE OR REPLACE VIEW vacancy_catalog AS
SELECT s.id AS source_id, s.external_id AS id, s.title AS name,
       COALESCE(s.content, '') AS description, COALESCE(s.source_url, '') AS alternate_url,
       s.vacancy_published_at AS published_at,
       COALESCE(d.details->>'area', '') AS area,
       COALESCE(d.details->>'employer_name', '') AS employer_name,
       COALESCE(d.details->>'employer_id', '') AS employer_id,
       COALESCE(d.details->>'employer_logo', '') AS employer_logo,
       COALESCE(d.details->'parsed_skills', '[]'::jsonb) AS parsed_skills,
       COALESCE(d.details->>'experience', '') AS experience_id,
       CASE
         WHEN lower(d.details->>'experience') IN ('noexperience','no_experience','less1','less','junior') THEN 'junior'
         WHEN lower(d.details->>'experience') IN ('between1and3','between3and6','middle') THEN 'middle'
         WHEN lower(d.details->>'experience') IN ('morethan6','morethan10','more','senior') THEN 'senior'
         WHEN lower(s.title) ~ 'junior|младший' THEN 'junior'
         WHEN lower(s.title) ~ 'senior|старший|ведущий' THEN 'senior'
         ELSE 'middle'
       END AS experience,
       (d.details #>> '{salary,from}')::bigint AS salary_from,
       (d.details #>> '{salary,to}')::bigint AS salary_to,
       COALESCE(d.details #>> '{salary,currency}', '') AS salary_currency,
       COALESCE(d.details->'key_skills', '[]'::jsonb) AS key_skills,
       COALESCE(d.details->'snippet', '{}'::jsonb) AS snippet
FROM sources s
LEFT JOIN vacancy_market_details d ON d.source_id = s.id
WHERE s.type = 'vacancy' AND s.provider = 'hh' AND s.external_id IS NOT NULL;

DROP FUNCTION derive_vacancy_grade(text, text);
