-- +goose Up

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE roles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code TEXT NOT NULL UNIQUE CHECK (btrim(code) <> ''),
    name TEXT NOT NULL UNIQUE CHECK (btrim(name) <> ''),
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT NOT NULL UNIQUE CHECK (btrim(email) <> ''),
    display_name TEXT NOT NULL CHECK (btrim(display_name) <> ''),
    role_id UUID REFERENCES roles(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'done', 'failed')),
    profession TEXT NOT NULL CHECK (btrim(profession) <> ''),
    root_node_id UUID,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (finished_at IS NULL OR started_at IS NULL OR finished_at >= started_at)
);

CREATE TABLE sources (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID REFERENCES runs(id) ON DELETE SET NULL,
    type TEXT NOT NULL
        CHECK (type IN ('vacancy', 'interview', 'standard', 'document')),
    title TEXT NOT NULL CHECK (btrim(title) <> ''),
    content TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE nodes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    parent_id UUID REFERENCES nodes(id) ON DELETE CASCADE,
    level SMALLINT NOT NULL DEFAULT 0 CHECK (level >= 0),
    type TEXT NOT NULL
        CHECK (type IN ('role', 'function', 'task', 'action')),
    title TEXT NOT NULL CHECK (btrim(title) <> ''),
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE runs
    ADD CONSTRAINT fk_runs_root_node
    FOREIGN KEY (root_node_id)
    REFERENCES nodes(id)
    ON DELETE SET NULL
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE evidence (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    source_id UUID NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    quote TEXT NOT NULL,
    approved BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    version INTEGER NOT NULL CHECK (version > 0),
    tree JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id, version)
);

CREATE TABLE agent_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID REFERENCES runs(id) ON DELETE CASCADE,
    message_type TEXT NOT NULL CHECK (btrim(message_type) <> ''),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    hmac_signature TEXT,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'sent', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE idempotency_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key TEXT NOT NULL,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    request_hash TEXT,
    response_code INTEGER,
    response_body JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ
);

CREATE INDEX idx_sources_run_id ON sources(run_id);
CREATE INDEX idx_nodes_run_id ON nodes(run_id);
CREATE INDEX idx_nodes_parent_id ON nodes(parent_id);
CREATE INDEX idx_nodes_level ON nodes(level);
CREATE INDEX idx_evidence_node_id ON evidence(node_id);
CREATE INDEX idx_evidence_source_id ON evidence(source_id);
CREATE INDEX idx_snapshots_run_id ON snapshots(run_id);
CREATE INDEX idx_agent_messages_run_id ON agent_messages(run_id);

CREATE UNIQUE INDEX uq_nodes_parent_id_title
    ON nodes(parent_id, title);

CREATE UNIQUE INDEX uq_idempotency_keys_key
    ON idempotency_keys(key);

CREATE UNIQUE INDEX uq_agent_messages_run_completed
    ON agent_messages(run_id, message_type)
    WHERE message_type = 'run.completed';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enqueue_run_completed()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.status = 'done'
       AND (TG_OP = 'INSERT' OR OLD.status IS DISTINCT FROM 'done') THEN
        INSERT INTO agent_messages (run_id, message_type, payload)
        VALUES (
            NEW.id,
            'run.completed',
            jsonb_build_object(
                'event', 'run.completed',
                'run_id', NEW.id,
                'profession', NEW.profession,
                'finished_at', NEW.finished_at
            )
        )
        ON CONFLICT DO NOTHING;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_runs_enqueue_completed
AFTER INSERT OR UPDATE OF status ON runs
FOR EACH ROW
EXECUTE FUNCTION enqueue_run_completed();

-- +goose Down

DROP TRIGGER IF EXISTS trg_runs_enqueue_completed ON runs;
DROP FUNCTION IF EXISTS enqueue_run_completed;

DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS agent_messages;
DROP TABLE IF EXISTS snapshots;
DROP TABLE IF EXISTS evidence;

ALTER TABLE runs DROP CONSTRAINT IF EXISTS fk_runs_root_node;

DROP TABLE IF EXISTS nodes;
DROP TABLE IF EXISTS sources;
DROP TABLE IF EXISTS runs;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS roles;
