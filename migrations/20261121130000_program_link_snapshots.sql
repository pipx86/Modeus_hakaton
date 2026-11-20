-- +goose Up
-- +goose StatementBegin
-- Program links are versioned with the exact immutable tree snapshot reviewed
-- by the methodist. Legacy links attach to the latest available snapshot.
ALTER TABLE activity_program_links ADD COLUMN snapshot_id UUID;
UPDATE activity_program_links l
SET snapshot_id = (
    SELECT s.id FROM snapshots s WHERE s.run_id=l.run_id ORDER BY s.version DESC LIMIT 1
);

ALTER TABLE activity_program_links
    ADD CONSTRAINT activity_program_links_node_run_fk
        FOREIGN KEY (node_id,run_id) REFERENCES nodes(id,run_id) ON DELETE CASCADE,
    ADD CONSTRAINT activity_program_links_snapshot_run_fk
        FOREIGN KEY (snapshot_id,run_id) REFERENCES snapshots(id,run_id) ON DELETE CASCADE;

ALTER TABLE activity_program_links DROP CONSTRAINT activity_program_links_run_id_node_id_competency_id_key;
ALTER TABLE activity_program_links ADD CONSTRAINT activity_program_links_versioned_unique
    UNIQUE (run_id,snapshot_id,node_id,competency_id);
CREATE INDEX activity_program_links_snapshot_idx ON activity_program_links(run_id,snapshot_id,status);

CREATE FUNCTION validate_activity_program_link_target() RETURNS TRIGGER AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM program_competencies c
        WHERE c.id=NEW.competency_id AND c.program_id=NEW.program_id
    ) THEN
        RAISE EXCEPTION 'PROGRAM_LINK_COMPETENCY_MISMATCH' USING ERRCODE='23514';
    END IF;
    IF NEW.ksa_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM competency_ksa k WHERE k.id=NEW.ksa_id AND k.competency_id=NEW.competency_id
    ) THEN
        RAISE EXCEPTION 'PROGRAM_LINK_KSA_MISMATCH' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_validate_activity_program_link_target
BEFORE INSERT OR UPDATE OF program_id,competency_id,ksa_id ON activity_program_links
FOR EACH ROW EXECUTE FUNCTION validate_activity_program_link_target();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_validate_activity_program_link_target ON activity_program_links;
DROP FUNCTION IF EXISTS validate_activity_program_link_target();
DROP INDEX IF EXISTS activity_program_links_snapshot_idx;
ALTER TABLE activity_program_links DROP CONSTRAINT IF EXISTS activity_program_links_versioned_unique;
ALTER TABLE activity_program_links ADD CONSTRAINT activity_program_links_run_id_node_id_competency_id_key UNIQUE(run_id,node_id,competency_id);
ALTER TABLE activity_program_links DROP CONSTRAINT IF EXISTS activity_program_links_snapshot_run_fk;
ALTER TABLE activity_program_links DROP CONSTRAINT IF EXISTS activity_program_links_node_run_fk;
ALTER TABLE activity_program_links DROP COLUMN IF EXISTS snapshot_id;
-- +goose StatementEnd
