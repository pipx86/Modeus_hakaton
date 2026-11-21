-- +goose Up
-- Канонический ключ программы (org/code/year/form) отделяет стабильную
-- идентичность программы от её названия и позволяет хранить несколько версий.
ALTER TABLE education_programs ADD COLUMN IF NOT EXISTS program_key TEXT;
ALTER TABLE education_programs ADD COLUMN IF NOT EXISTS version INT NOT NULL DEFAULT 1;

CREATE UNIQUE INDEX IF NOT EXISTS uq_education_programs_program_key
    ON education_programs(program_key)
    WHERE program_key IS NOT NULL;

-- Backfill: до появления program_key связь с файловым импортом выражалась
-- только через sources.external_id = 'education:<key>'. Восстанавливаем ключ из
-- этого идентификатора, чтобы старые программы продолжили обновляться по
-- program_key, а не только по program_sources.
WITH derived AS (
    SELECT DISTINCT ON (ps.program_id)
        ps.program_id,
        NULLIF(btrim(substr(s.external_id, length('education:') + 1)), '') AS program_key
    FROM program_sources ps
    JOIN sources s ON s.id = ps.source_id
    WHERE s.provider = 'file'
      AND s.external_id LIKE 'education:%'
    ORDER BY ps.program_id, s.created_at, s.id
),
picked AS (
    -- Один source мог быть связан с несколькими программами. Уникальный индекс
    -- допускает только одного владельца ключа, поэтому берём первую программу.
    SELECT DISTINCT ON (program_key) program_id, program_key
    FROM derived
    WHERE program_key IS NOT NULL
    ORDER BY program_key, program_id
)
UPDATE education_programs p
SET program_key = picked.program_key
FROM picked
WHERE p.id = picked.program_id
  AND p.program_key IS NULL;

-- Приводим файловые источники к тому же виду. DISTINCT ON делает выбор
-- детерминированным, если source связан с несколькими программами, а NOT EXISTS
-- защищает от конфликта с уже существующим external_id у другого source.
WITH targets AS (
    SELECT DISTINCT ON (s.id)
        s.id,
        s.type,
        'education:' || p.program_key AS external_id
    FROM sources s
    JOIN program_sources ps ON ps.source_id = s.id
    JOIN education_programs p ON p.id = ps.program_id
    WHERE s.provider = 'file'
      AND p.program_key IS NOT NULL
      AND s.external_id IS DISTINCT FROM 'education:' || p.program_key
    ORDER BY s.id, p.created_at, p.id
)
UPDATE sources s
SET external_id = targets.external_id,
    updated_at = now()
FROM targets
WHERE s.id = targets.id
  AND NOT EXISTS (
      SELECT 1
      FROM sources other
      WHERE other.provider = 'file'
        AND other.type = targets.type
        AND other.external_id = targets.external_id
  );

-- Реестр URL, которые воркер проверяет по расписанию. program_key обязан быть
-- каноническим: это проверяет API/воркер, а в БД дополнительно запрещаем
-- пустые значения.
CREATE TABLE program_watch_sources (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    program_key TEXT NOT NULL,
    url TEXT NOT NULL,
    doc_format TEXT,
    interval_hours INT NOT NULL DEFAULT 24 CHECK (interval_hours BETWEEN 1 AND 720),
    enabled BOOL NOT NULL DEFAULT true,
    etag TEXT,
    last_modified TEXT,
    last_hash TEXT,
    last_checked_at TIMESTAMPTZ,
    last_status TEXT,
    last_error TEXT,
    draft_id UUID,
    -- Сколько проверок подряд завершились ошибкой. Нужен для экспоненциальной
    -- паузы: next check = last_checked_at + interval_hours * 2^failure_count.
    failure_count INT NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (program_key, url)
);

CREATE INDEX program_watch_sources_due_idx
    ON program_watch_sources(enabled, last_checked_at);
CREATE INDEX program_watch_sources_program_key_idx
    ON program_watch_sources(program_key);

-- +goose Down
DROP TABLE IF EXISTS program_watch_sources;
DROP INDEX IF EXISTS uq_education_programs_program_key;
ALTER TABLE education_programs DROP COLUMN IF EXISTS version;
ALTER TABLE education_programs DROP COLUMN IF EXISTS program_key;
