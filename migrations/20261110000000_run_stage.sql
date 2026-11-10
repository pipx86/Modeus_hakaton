-- +goose Up
-- Сохраняем этап для SSE и восстановления оркестратора после перезапуска.
ALTER TABLE runs ADD COLUMN stage TEXT NOT NULL DEFAULT 'created'
    CHECK(stage IN ('created','interview','analysis','synthesis','review','done','failed'));
UPDATE runs SET stage = status WHERE status IN ('done','failed');

-- +goose Down
-- Откат теряет сведения об этапах исследований.
ALTER TABLE runs DROP COLUMN stage;
