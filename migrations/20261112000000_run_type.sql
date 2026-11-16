-- +goose Up
-- Существующие исследования относятся к пакетному типу.
ALTER TABLE runs ADD COLUMN type TEXT NOT NULL DEFAULT 'batch'
    CHECK (type IN ('batch', 'chat'));

-- +goose Down
-- Откат удаляет сведения о типе исследования.
ALTER TABLE runs DROP COLUMN type;
