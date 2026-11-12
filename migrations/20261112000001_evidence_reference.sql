-- +goose Up
-- Идентификатор фрагмента может быть как UUID поиска, так и номером сообщения интервью.
ALTER TABLE evidence ADD COLUMN ref_id TEXT
    CHECK (ref_id IS NULL OR btrim(ref_id) <> '');

-- +goose Down
ALTER TABLE evidence DROP COLUMN ref_id;
