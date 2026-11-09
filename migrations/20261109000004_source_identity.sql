-- +goose Up
-- Внешние идентификаторы уникальны в пределах поставщика и типа материала.
-- Для старых и ручных источников поставщик может оставаться неизвестным.
ALTER TABLE sources ADD COLUMN provider TEXT
    CHECK (provider IS NULL OR (btrim(provider) <> '' AND provider = lower(btrim(provider))));
CREATE UNIQUE INDEX uq_sources_provider_type_external_id
    ON sources(provider, type, external_id)
    WHERE provider IS NOT NULL AND external_id IS NOT NULL;

-- +goose Down
DROP INDEX uq_sources_provider_type_external_id;
ALTER TABLE sources DROP COLUMN provider;
