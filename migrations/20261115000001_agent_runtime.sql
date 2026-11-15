-- +goose Up
-- Runtime fields required by the agent/LLM pipeline.

-- A run may finish in degraded mode without being marked failed.
ALTER TABLE runs DROP CONSTRAINT IF EXISTS runs_status_check;
ALTER TABLE runs ADD CONSTRAINT runs_status_check
    CHECK (status IN ('pending', 'running', 'done', 'failed', 'degraded'));

ALTER TABLE runs ADD COLUMN degraded_reason TEXT;

-- Retry/repair attempts are part of the request identity.
ALTER TABLE llm_requests ADD COLUMN attempt INTEGER NOT NULL DEFAULT 1
    CHECK (attempt >= 1);
ALTER TABLE llm_requests ADD COLUMN max_attempts INTEGER NOT NULL DEFAULT 3
    CHECK (max_attempts >= 1);

-- A PII finding may point at a concrete response.
ALTER TABLE pii_findings ADD CONSTRAINT pii_findings_response_fk
    FOREIGN KEY (response_id) REFERENCES llm_responses(request_id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE pii_findings DROP CONSTRAINT IF EXISTS pii_findings_response_fk;

ALTER TABLE llm_requests DROP COLUMN IF EXISTS max_attempts;
ALTER TABLE llm_requests DROP COLUMN IF EXISTS attempt;

ALTER TABLE runs DROP COLUMN IF EXISTS degraded_reason;

ALTER TABLE runs DROP CONSTRAINT IF EXISTS runs_status_check;
ALTER TABLE runs ADD CONSTRAINT runs_status_check
    CHECK (status IN ('pending', 'running', 'done', 'failed'));
