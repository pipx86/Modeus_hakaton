-- +goose Up
-- LLM observability and prompt registry.
-- Additive: existing runs, nodes, evidence and agent_messages stay intact.

CREATE TABLE prompt_versions (
    id TEXT PRIMARY KEY CHECK (btrim(id) <> ''),
    agent TEXT NOT NULL CHECK (agent IN ('interviewer', 'analyst', 'synthesizer')),
    version TEXT NOT NULL CHECK (btrim(version) <> ''),
    prompt_hash CHAR(64) NOT NULL CHECK (btrim(prompt_hash) <> ''),
    system_text TEXT NOT NULL CHECK (btrim(system_text) <> ''),
    examples_text TEXT,
    status TEXT NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'active', 'archived')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    activated_at TIMESTAMPTZ,
    UNIQUE (agent, version, prompt_hash)
);

CREATE UNIQUE INDEX prompt_versions_one_active_idx
    ON prompt_versions (agent)
    WHERE status = 'active';

CREATE TABLE prompt_activations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent TEXT NOT NULL CHECK (agent IN ('interviewer', 'analyst', 'synthesizer')),
    prompt_version_id TEXT NOT NULL REFERENCES prompt_versions(id) ON DELETE RESTRICT,
    activated_by TEXT,
    reason TEXT,
    activated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX prompt_activations_agent_created_idx
    ON prompt_activations (agent, activated_at DESC);

CREATE TABLE llm_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trace_id TEXT NOT NULL CHECK (btrim(trace_id) <> ''),
    run_id UUID REFERENCES runs(id) ON DELETE SET NULL,
    agent TEXT NOT NULL CHECK (agent IN ('interviewer', 'analyst', 'synthesizer')),
    provider TEXT NOT NULL CHECK (btrim(provider) <> ''),
    model TEXT NOT NULL CHECK (btrim(model) <> ''),
    prompt_version_id TEXT REFERENCES prompt_versions(id) ON DELETE SET NULL,
    request_hash CHAR(64),
    messages JSONB,
    params JSONB,
    status TEXT NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'ok', 'error', 'timeout', 'degraded')),
    error_code TEXT,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    latency_ms INTEGER CHECK (latency_ms IS NULL OR latency_ms >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (finished_at IS NULL OR started_at IS NULL OR finished_at >= started_at)
);

CREATE INDEX llm_requests_trace_idx ON llm_requests (trace_id);
CREATE INDEX llm_requests_run_created_idx ON llm_requests (run_id, created_at DESC);
CREATE INDEX llm_requests_agent_created_idx ON llm_requests (agent, created_at DESC);
CREATE INDEX llm_requests_status_created_idx ON llm_requests (status, created_at DESC);

CREATE TABLE llm_responses (
    request_id UUID PRIMARY KEY REFERENCES llm_requests(id) ON DELETE CASCADE,
    raw_text TEXT,
    parsed_json JSONB,
    validation_status TEXT CHECK (validation_status IS NULL OR validation_status IN ('valid', 'invalid', 'repaired')),
    repair_attempt INTEGER NOT NULL DEFAULT 0 CHECK (repair_attempt >= 0),
    finish_reason TEXT,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE llm_usage (
    request_id UUID PRIMARY KEY REFERENCES llm_requests(id) ON DELETE CASCADE,
    prompt_tokens INTEGER NOT NULL DEFAULT 0 CHECK (prompt_tokens >= 0),
    completion_tokens INTEGER NOT NULL DEFAULT 0 CHECK (completion_tokens >= 0),
    total_tokens INTEGER NOT NULL DEFAULT 0 CHECK (total_tokens >= 0),
    cost_usd NUMERIC(12, 6) CHECK (cost_usd IS NULL OR cost_usd >= 0),
    reasoning_effort TEXT,
    latency_ms INTEGER CHECK (latency_ms IS NULL OR latency_ms >= 0)
);

CREATE TABLE llm_evaluations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id UUID NOT NULL REFERENCES llm_requests(id) ON DELETE CASCADE,
    evaluator TEXT NOT NULL CHECK (btrim(evaluator) <> ''),
    criterion TEXT NOT NULL CHECK (btrim(criterion) <> ''),
    score NUMERIC(6, 3) CHECK (score IS NULL OR (score >= 0 AND score <= 1)),
    details JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX llm_evaluations_request_idx ON llm_evaluations (request_id);

CREATE TABLE pii_findings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id UUID NOT NULL REFERENCES llm_requests(id) ON DELETE CASCADE,
    response_id UUID,
    pii_type TEXT NOT NULL CHECK (btrim(pii_type) <> ''),
    hash TEXT NOT NULL CHECK (btrim(hash) <> ''),
    start_offset INTEGER CHECK (start_offset IS NULL OR start_offset >= 0),
    end_offset INTEGER CHECK (end_offset IS NULL OR end_offset >= 0),
    action TEXT NOT NULL DEFAULT 'redacted'
        CHECK (action IN ('redacted', 'blocked', 'allowed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (end_offset IS NULL OR start_offset IS NULL OR end_offset >= start_offset)
);

CREATE INDEX pii_findings_request_idx ON pii_findings (request_id);

ALTER TABLE agent_messages ADD COLUMN dedup_key TEXT;

CREATE UNIQUE INDEX uq_agent_messages_run_dedup
    ON agent_messages (run_id, dedup_key)
    WHERE dedup_key IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS uq_agent_messages_run_dedup;
ALTER TABLE agent_messages DROP COLUMN IF EXISTS dedup_key;

DROP TABLE IF EXISTS pii_findings;
DROP TABLE IF EXISTS llm_evaluations;
DROP TABLE IF EXISTS llm_usage;
DROP TABLE IF EXISTS llm_responses;
DROP TABLE IF EXISTS llm_requests;
DROP TABLE IF EXISTS prompt_activations;
DROP TABLE IF EXISTS prompt_versions;
