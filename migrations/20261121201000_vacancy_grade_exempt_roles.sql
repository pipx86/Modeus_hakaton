-- +goose Up
-- DevOps and data analyst are pilot roles without a mandatory research grade.
-- Keep their vacancies even when the provider does not supply a grade. The raw
-- provider experience remains available as experience_id; experience is blank
-- so these vacancies do not alter junior/middle/senior statistics.
CREATE OR REPLACE FUNCTION vacancy_allows_ungraded(vacancy_title text)
RETURNS boolean
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
AS $function$
    SELECT lower(COALESCE(vacancy_title, '')) ~
        $pattern$\m(dev[[:space:]-]?ops|девопс|data[[:space:]-]+(analyst|аналитик)|аналитик[[:space:]]+данных)\M$pattern$;
$function$;

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
       CASE WHEN vacancy_allows_ungraded(s.title) THEN ''
            ELSE derive_vacancy_grade(COALESCE(d.details->>'experience', ''), s.title)
       END AS experience,
       (d.details #>> '{salary,from}')::bigint AS salary_from,
       (d.details #>> '{salary,to}')::bigint AS salary_to,
       COALESCE(d.details #>> '{salary,currency}', '') AS salary_currency,
       COALESCE(d.details->'key_skills', '[]'::jsonb) AS key_skills,
       COALESCE(d.details->'snippet', '{}'::jsonb) AS snippet
FROM sources s
LEFT JOIN vacancy_market_details d ON d.source_id = s.id
WHERE s.type = 'vacancy' AND s.provider = 'hh' AND s.external_id IS NOT NULL
  AND (vacancy_allows_ungraded(s.title) OR
       derive_vacancy_grade(COALESCE(d.details->>'experience', ''), s.title) <> '');

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

DROP FUNCTION vacancy_allows_ungraded(text);
