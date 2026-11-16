-- +goose Up
-- Этот пакет применяется отдельно от основных миграций и требует pgvector.
CREATE EXTENSION IF NOT EXISTS vector;

-- Отдельный фрагмент источника, который может быть возвращён поиском.
CREATE TABLE source_fragments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id UUID NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    quote TEXT NOT NULL CHECK (btrim(quote) <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source_id, ordinal)
);

-- Векторы разных моделей хранятся раздельно и не сравниваются между собой.
CREATE TABLE fragment_embeddings (
    fragment_id UUID NOT NULL REFERENCES source_fragments(id) ON DELETE CASCADE,
    model TEXT NOT NULL CHECK (btrim(model) <> ''),
    embedding vector(768) NOT NULL CHECK (vector_norm(embedding) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (fragment_id, model)
);
CREATE INDEX idx_fragment_embeddings_cosine
    ON fragment_embeddings USING hnsw (embedding vector_cosine_ops);

-- Возвращаем идентификатор фрагмента, его источник, текст и косинусное сходство.
-- +goose StatementBegin
CREATE FUNCTION find_relevant_fragments(
    p_run_id UUID, p_embedding vector(768), p_model TEXT, p_limit INTEGER
) RETURNS TABLE (ref_id UUID, source_id UUID, quote TEXT, score DOUBLE PRECISION)
LANGUAGE sql STABLE AS $$
    SELECT f.id, f.source_id, f.quote, 1 - (e.embedding <=> p_embedding) AS score
    FROM fragment_embeddings e
    JOIN source_fragments f ON f.id = e.fragment_id
    JOIN run_sources rs ON rs.source_id = f.source_id
    WHERE rs.run_id = p_run_id AND e.model = p_model
    ORDER BY e.embedding <=> p_embedding
    LIMIT GREATEST(0, LEAST(COALESCE(p_limit, 0), 100));
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS find_relevant_fragments(UUID, vector, TEXT, INTEGER);
DROP TABLE IF EXISTS fragment_embeddings;
DROP TABLE IF EXISTS source_fragments;
-- Расширение может использоваться другими компонентами; не удаляем его.
