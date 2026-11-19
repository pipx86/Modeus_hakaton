-- +goose Up
CREATE TABLE vacancy_market_details (
    source_id UUID PRIMARY KEY REFERENCES sources(id) ON DELETE CASCADE,
    details JSONB NOT NULL CHECK (jsonb_typeof(details) = 'object')
);

CREATE VIEW vacancy_catalog AS
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

-- +goose Down
DROP VIEW vacancy_catalog;
DROP TABLE vacancy_market_details;
