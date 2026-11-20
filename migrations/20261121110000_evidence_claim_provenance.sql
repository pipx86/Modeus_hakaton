-- +goose Up
-- +goose StatementBegin

-- Each quote records whether the activity statement is present verbatim in
-- its source or is an interpretation grounded by that quote. Historical rows
-- remain explicitly unclassified instead of being presented as direct claims.
ALTER TABLE evidence
    ADD COLUMN claim_origin TEXT NOT NULL DEFAULT 'unclassified'
        CHECK (claim_origin IN ('source_statement', 'model_interpretation', 'unclassified')),
    ADD COLUMN interview_id UUID REFERENCES run_interviews(id) ON DELETE SET NULL;

-- Backfill the only interview available to each legacy run. New evidence is
-- linked explicitly by the finalizer and never guessed from a run-wide chat.
UPDATE evidence e
SET interview_id = i.id
FROM nodes n, run_interviews i, sources s
WHERE n.id = e.node_id
  AND i.run_id = n.run_id
  AND s.id = e.source_id
  AND s.type = 'interview'
  AND (SELECT count(*) FROM run_interviews ri WHERE ri.run_id = n.run_id) = 1;

-- Composite keys let follow-up tables keep tree snapshots scoped to one run;
-- nodes already have the equivalent unique key from migration 20261109000001.
ALTER TABLE snapshots ADD CONSTRAINT snapshots_id_run_unique UNIQUE (id, run_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE snapshots DROP CONSTRAINT IF EXISTS snapshots_id_run_unique;
ALTER TABLE evidence DROP COLUMN IF EXISTS interview_id;
ALTER TABLE evidence DROP COLUMN IF EXISTS claim_origin;
-- +goose StatementEnd
