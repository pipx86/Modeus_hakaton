-- +goose Up
CREATE TABLE program_import_drafts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status TEXT NOT NULL CHECK (status IN ('draft', 'published', 'rejected')),
    program_key TEXT NOT NULL,
    source_name TEXT NOT NULL,
    source_format TEXT NOT NULL,
    program_json JSONB NOT NULL,
    report_json JSONB NOT NULL,
    warnings JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at TIMESTAMPTZ,
    decided_by UUID,
    error TEXT
);

CREATE INDEX program_import_drafts_status_idx ON program_import_drafts(status);
CREATE INDEX program_import_drafts_created_at_idx ON program_import_drafts(created_at DESC);

-- Тип компетенции (УК/ОПК/ПК) хранится отдельной колонкой, а не выводится из
-- кода: один и тот же код может встречаться в разных программах с разным типом.
ALTER TABLE program_competencies ADD COLUMN IF NOT EXISTS type TEXT;
ALTER TABLE program_competencies DROP CONSTRAINT IF EXISTS program_competencies_type_check;
ALTER TABLE program_competencies ADD CONSTRAINT program_competencies_type_check
    CHECK (type IS NULL OR type IN ('universal', 'general_professional', 'professional', 'other'));

-- +goose Down
ALTER TABLE program_competencies DROP CONSTRAINT IF EXISTS program_competencies_type_check;
ALTER TABLE program_competencies DROP COLUMN IF EXISTS type;
DROP TABLE IF EXISTS program_import_drafts;
