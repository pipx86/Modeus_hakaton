-- +goose Up

-- Происхождение и состояние обработки входных материалов.
ALTER TABLE sources
    ADD COLUMN source_url TEXT,
    ADD COLUMN external_id TEXT,
    ADD COLUMN raw_payload JSONB,
    ADD COLUMN processing_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (processing_status IN ('pending', 'processing', 'processed', 'failed')),
    ADD COLUMN processing_error TEXT,
    ADD COLUMN processed_at TIMESTAMPTZ;

-- Внешний идентификатор может повторяться между разными поставщиками.
CREATE INDEX idx_sources_type_external_id ON sources (type, external_id)
    WHERE external_id IS NOT NULL;
CREATE INDEX idx_sources_processing_status ON sources (processing_status);

-- Причина ошибки исследования для диагностики и повторного запуска.
ALTER TABLE runs ADD COLUMN error_message TEXT;

-- +goose Down

DROP INDEX idx_sources_processing_status;
DROP INDEX idx_sources_type_external_id;
ALTER TABLE runs DROP COLUMN error_message;
ALTER TABLE sources
    DROP COLUMN processed_at,
    DROP COLUMN processing_error,
    DROP COLUMN processing_status,
    DROP COLUMN raw_payload,
    DROP COLUMN external_id,
    DROP COLUMN source_url;
