-- +goose Up
-- Один материал может использоваться в нескольких исследованиях.
CREATE TABLE run_sources (
    run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    source_id UUID NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, source_id)
);
CREATE INDEX idx_run_sources_source_id ON run_sources(source_id);

-- Переносим существующие связи из прежнего поля.
INSERT INTO run_sources (run_id, source_id)
SELECT run_id, id FROM sources WHERE run_id IS NOT NULL
ON CONFLICT DO NOTHING;

-- Сохраняем совместимость с кодом, который пока записывает sources.run_id.
-- Новые связи добавляются, старые при смене поля не удаляются.
-- +goose StatementBegin
CREATE FUNCTION link_source_legacy_run() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.run_id IS NOT NULL THEN
        INSERT INTO run_sources (run_id, source_id)
        VALUES (NEW.run_id, NEW.id)
        ON CONFLICT DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER trg_sources_link_legacy_run
AFTER INSERT OR UPDATE OF run_id ON sources
FOR EACH ROW EXECUTE FUNCTION link_source_legacy_run();

-- Источник нельзя удалить, пока на него ссылаются подтверждения.
ALTER TABLE evidence DROP CONSTRAINT evidence_source_id_fkey;
ALTER TABLE evidence ADD CONSTRAINT evidence_source_id_fkey
    FOREIGN KEY (source_id) REFERENCES sources(id) ON DELETE RESTRICT;

-- +goose Down
-- При откате дополнительные связи run_sources будут удалены.
ALTER TABLE evidence DROP CONSTRAINT evidence_source_id_fkey;
ALTER TABLE evidence ADD CONSTRAINT evidence_source_id_fkey
    FOREIGN KEY (source_id) REFERENCES sources(id) ON DELETE CASCADE;
DROP TRIGGER IF EXISTS trg_sources_link_legacy_run ON sources;
DROP FUNCTION IF EXISTS link_source_legacy_run;
DROP TABLE IF EXISTS run_sources;
