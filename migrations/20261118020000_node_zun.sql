-- +goose Up
-- Знания, умения и навыки узла дерева: генерируются после синтеза.
ALTER TABLE nodes
    ADD COLUMN IF NOT EXISTS zun JSONB;

-- +goose Down
ALTER TABLE nodes
    DROP COLUMN IF EXISTS zun;
