-- +goose Up
CREATE TABLE source_content_changes (
    id BIGSERIAL PRIMARY KEY,
    source_id UUID NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX source_content_changes_source_time_idx ON source_content_changes(source_id, changed_at DESC);

-- +goose StatementBegin
CREATE FUNCTION record_source_content_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (OLD.title, OLD.content, OLD.source_url) IS DISTINCT FROM (NEW.title, NEW.content, NEW.source_url) THEN
        INSERT INTO source_content_changes(source_id) VALUES (NEW.id);
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER source_content_change AFTER UPDATE ON sources
FOR EACH ROW EXECUTE FUNCTION record_source_content_change();

-- +goose Down
DROP TRIGGER IF EXISTS source_content_change ON sources;
DROP FUNCTION IF EXISTS record_source_content_change();
DROP TABLE IF EXISTS source_content_changes;
