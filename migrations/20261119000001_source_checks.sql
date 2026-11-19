-- +goose Up
-- Технические признаки источника: они не доказывают истинность содержания.
CREATE TABLE source_checks (
    source_id UUID PRIMARY KEY REFERENCES sources(id) ON DELETE CASCADE,
    content_present BOOLEAN NOT NULL,
    origin_present BOOLEAN NOT NULL,
    url_format TEXT NOT NULL CHECK (url_format IN ('missing', 'valid', 'invalid')),
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Совпадение цитаты проверяется отдельно от свойств самого источника.
CREATE TABLE evidence_quote_checks (
    evidence_id UUID PRIMARY KEY REFERENCES evidence(id) ON DELETE CASCADE,
    result TEXT NOT NULL CHECK (result IN ('matched', 'not_matched', 'unavailable')),
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose StatementBegin
CREATE FUNCTION refresh_source_checks() RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO source_checks(source_id, content_present, origin_present, url_format, checked_at)
    VALUES (NEW.id,
        COALESCE(btrim(NEW.content) <> '', FALSE),
        COALESCE(btrim(NEW.provider) <> '' AND btrim(NEW.external_id) <> '', FALSE),
        CASE WHEN COALESCE(btrim(NEW.source_url), '') = '' THEN 'missing'
             WHEN btrim(NEW.source_url) ~* '^https?://[^[:space:]/?#]+([/?#][^[:space:]]*)?$' THEN 'valid'
             ELSE 'invalid' END,
        clock_timestamp())
    ON CONFLICT (source_id) DO UPDATE SET
        content_present = EXCLUDED.content_present,
        origin_present = EXCLUDED.origin_present,
        url_format = EXCLUDED.url_format,
        checked_at = EXCLUDED.checked_at;

    INSERT INTO evidence_quote_checks(evidence_id, result, checked_at)
    SELECT e.id,
        CASE WHEN COALESCE(btrim(NEW.content), '') = '' THEN 'unavailable'
             WHEN COALESCE(btrim(e.quote), '') <> '' AND strpos(NEW.content, e.quote) > 0 THEN 'matched'
             ELSE 'not_matched' END,
        clock_timestamp()
    FROM evidence e WHERE e.source_id = NEW.id
    ON CONFLICT (evidence_id) DO UPDATE SET result = EXCLUDED.result, checked_at = EXCLUDED.checked_at;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION refresh_evidence_quote_check() RETURNS TRIGGER AS $$
DECLARE source_content TEXT;
BEGIN
    SELECT content INTO source_content FROM sources WHERE id = NEW.source_id FOR SHARE;
    INSERT INTO evidence_quote_checks(evidence_id, result, checked_at)
    VALUES (NEW.id,
        CASE WHEN COALESCE(btrim(source_content), '') = '' THEN 'unavailable'
             WHEN COALESCE(btrim(NEW.quote), '') <> '' AND strpos(source_content, NEW.quote) > 0 THEN 'matched'
             ELSE 'not_matched' END,
        clock_timestamp())
    ON CONFLICT (evidence_id) DO UPDATE SET result = EXCLUDED.result, checked_at = EXCLUDED.checked_at;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_source_checks
AFTER INSERT OR UPDATE OF content, provider, external_id, source_url ON sources
FOR EACH ROW EXECUTE FUNCTION refresh_source_checks();

CREATE TRIGGER trg_evidence_quote_check
AFTER INSERT OR UPDATE OF quote, source_id ON evidence
FOR EACH ROW EXECUTE FUNCTION refresh_evidence_quote_check();

INSERT INTO source_checks(source_id, content_present, origin_present, url_format)
SELECT id, COALESCE(btrim(content) <> '', FALSE),
       COALESCE(btrim(provider) <> '' AND btrim(external_id) <> '', FALSE),
       CASE WHEN COALESCE(btrim(source_url), '') = '' THEN 'missing'
            WHEN btrim(source_url) ~* '^https?://[^[:space:]/?#]+([/?#][^[:space:]]*)?$' THEN 'valid'
            ELSE 'invalid' END
FROM sources;

INSERT INTO evidence_quote_checks(evidence_id, result)
SELECT e.id, CASE WHEN COALESCE(btrim(s.content), '') = '' THEN 'unavailable'
                  WHEN COALESCE(btrim(e.quote), '') <> '' AND strpos(s.content, e.quote) > 0 THEN 'matched'
                  ELSE 'not_matched' END
FROM evidence e JOIN sources s ON s.id = e.source_id;

-- +goose Down
DROP TRIGGER IF EXISTS trg_evidence_quote_check ON evidence;
DROP TRIGGER IF EXISTS trg_source_checks ON sources;
DROP FUNCTION IF EXISTS refresh_evidence_quote_check();
DROP FUNCTION IF EXISTS refresh_source_checks();
DROP TABLE evidence_quote_checks;
DROP TABLE source_checks;
