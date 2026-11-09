-- +goose Up
-- Сохраняем существующие связи подтверждений с исследованиями.
INSERT INTO run_sources (run_id, source_id)
SELECT DISTINCT n.run_id, e.source_id FROM evidence e JOIN nodes n ON n.id = e.node_id
ON CONFLICT DO NOTHING;

ALTER TABLE evidence ADD COLUMN run_id UUID;
UPDATE evidence e SET run_id = n.run_id FROM nodes n WHERE n.id = e.node_id;
ALTER TABLE evidence ALTER COLUMN run_id SET NOT NULL;

ALTER TABLE evidence ADD CONSTRAINT fk_evidence_node_run
    FOREIGN KEY (node_id, run_id) REFERENCES nodes (id, run_id)
    ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE evidence ADD CONSTRAINT fk_evidence_run_source
    FOREIGN KEY (run_id, source_id) REFERENCES run_sources (run_id, source_id)
    DEFERRABLE INITIALLY DEFERRED;

-- Автоматически определяем исследование узла и добавляем связь источника.
-- +goose StatementBegin
CREATE FUNCTION link_evidence_source() RETURNS TRIGGER AS $$
BEGIN
    SELECT run_id INTO NEW.run_id FROM nodes WHERE id = NEW.node_id;
    IF NEW.run_id IS NULL THEN
        RAISE EXCEPTION 'Узел подтверждения не найден' USING ERRCODE = 'foreign_key_violation';
    END IF;
    INSERT INTO run_sources (run_id, source_id) VALUES (NEW.run_id, NEW.source_id)
    ON CONFLICT DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER trg_evidence_link_source
BEFORE INSERT OR UPDATE OF node_id, source_id, run_id ON evidence
FOR EACH ROW EXECUTE FUNCTION link_evidence_source();

-- +goose Down
DROP TRIGGER IF EXISTS trg_evidence_link_source ON evidence;
DROP FUNCTION IF EXISTS link_evidence_source;
ALTER TABLE evidence DROP CONSTRAINT fk_evidence_run_source;
ALTER TABLE evidence DROP CONSTRAINT fk_evidence_node_run;
ALTER TABLE evidence DROP COLUMN run_id;
