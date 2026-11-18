-- +goose Up
-- status: pending | approved | rejected
-- approved остаётся для обратной совместимости и синхронизируется со status.

ALTER TABLE evidence
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'pending';

UPDATE evidence
SET status = CASE WHEN approved THEN 'approved' ELSE 'pending' END
WHERE status = 'pending' AND approved = true;

ALTER TABLE evidence
    DROP CONSTRAINT IF EXISTS evidence_status_check;

ALTER TABLE evidence
    ADD CONSTRAINT evidence_status_check
    CHECK (status IN ('pending', 'approved', 'rejected'));

-- Держим approved в согласии со status при будущих UPDATE.
CREATE OR REPLACE FUNCTION evidence_sync_approved() RETURNS TRIGGER AS $$
BEGIN
    NEW.approved := (NEW.status = 'approved');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_evidence_sync_approved ON evidence;
CREATE TRIGGER trg_evidence_sync_approved
    BEFORE INSERT OR UPDATE OF status ON evidence
    FOR EACH ROW EXECUTE FUNCTION evidence_sync_approved();

CREATE INDEX IF NOT EXISTS idx_evidence_status ON evidence(status);

-- +goose Down
DROP TRIGGER IF EXISTS trg_evidence_sync_approved ON evidence;
DROP FUNCTION IF EXISTS evidence_sync_approved;
DROP INDEX IF EXISTS idx_evidence_status;
ALTER TABLE evidence DROP CONSTRAINT IF EXISTS evidence_status_check;
ALTER TABLE evidence DROP COLUMN IF EXISTS status;
