-- +goose Up
-- Отпечаток привязанного документа. Страница программы (HTML) может не меняться,
-- пока сам xlsx/pdf по ссылке обновляется, поэтому одного last_hash страницы
-- недостаточно: воркер проверяет документ отдельным условным GET и сравнивает
-- document_hash.
ALTER TABLE program_watch_sources ADD COLUMN IF NOT EXISTS document_url TEXT;
ALTER TABLE program_watch_sources ADD COLUMN IF NOT EXISTS document_hash TEXT;
ALTER TABLE program_watch_sources ADD COLUMN IF NOT EXISTS document_etag TEXT;
ALTER TABLE program_watch_sources ADD COLUMN IF NOT EXISTS document_last_modified TEXT;

COMMENT ON COLUMN program_watch_sources.document_url IS
    'URL документа программы: ссылка со страницы или прямой URL источника.';
COMMENT ON COLUMN program_watch_sources.document_hash IS
    'sha256 успешно разобранного документа; совпадение даёт статус unchanged.';
COMMENT ON COLUMN program_watch_sources.document_etag IS
    'ETag документа для If-None-Match при повторной проверке.';
COMMENT ON COLUMN program_watch_sources.document_last_modified IS
    'Last-Modified документа для If-Modified-Since при повторной проверке.';

-- +goose Down
ALTER TABLE program_watch_sources DROP COLUMN IF EXISTS document_last_modified;
ALTER TABLE program_watch_sources DROP COLUMN IF EXISTS document_etag;
ALTER TABLE program_watch_sources DROP COLUMN IF EXISTS document_hash;
ALTER TABLE program_watch_sources DROP COLUMN IF EXISTS document_url;
