-- +goose Up
-- BM25 по фрагментам источников, связанных с выбранным исследованием.
-- +goose StatementBegin
CREATE FUNCTION find_fragments_bm25(p_run_id UUID, p_query TEXT, p_limit INTEGER)
RETURNS TABLE (ref_id UUID, source_id UUID, quote TEXT, score DOUBLE PRECISION)
LANGUAGE sql STABLE AS $$
    WITH documents AS MATERIALIZED (
        SELECT f.id, f.source_id, f.quote, to_tsvector('russian', f.quote) AS tokens
        FROM source_fragments f
        JOIN run_sources rs ON rs.source_id=f.source_id
        WHERE rs.run_id=p_run_id
    ), query_terms AS (
        SELECT DISTINCT unnest(tsvector_to_array(to_tsvector('russian', COALESCE(p_query,'')))) AS term
    ), tokens AS MATERIALIZED (
        SELECT d.id, d.source_id, d.quote, u.lexeme,
               cardinality(u.positions)::DOUBLE PRECISION AS frequency
        FROM documents d CROSS JOIN LATERAL unnest(d.tokens) AS u
    ), document_lengths AS (
        SELECT d.id, d.source_id, d.quote, COALESCE(sum(t.frequency),0)::DOUBLE PRECISION AS length
        FROM documents d LEFT JOIN tokens t ON t.id=d.id
        GROUP BY d.id, d.source_id, d.quote
    ), corpus AS (
        SELECT count(*)::DOUBLE PRECISION AS document_count,
               avg(length)::DOUBLE PRECISION AS average_length
        FROM document_lengths
    ), term_counts AS (
        SELECT t.lexeme, count(DISTINCT t.id)::DOUBLE PRECISION AS document_frequency
        FROM tokens t JOIN query_terms q ON q.term=t.lexeme
        GROUP BY t.lexeme
    ), scores AS (
        SELECT d.id, d.source_id, d.quote,
               sum(ln(1 + (c.document_count - df.document_frequency + 0.5) / (df.document_frequency + 0.5)) *
                   ((t.frequency * 2.2) / (t.frequency + 1.2 * (0.25 + 0.75 * d.length / NULLIF(c.average_length,0)))))::DOUBLE PRECISION AS score
        FROM document_lengths d
        JOIN tokens t ON t.id=d.id
        JOIN term_counts df ON df.lexeme=t.lexeme
        CROSS JOIN corpus c
        GROUP BY d.id, d.source_id, d.quote
    )
    SELECT s.id, s.source_id, s.quote, s.score FROM scores s
    WHERE s.score IS NOT NULL
    ORDER BY s.score DESC, s.id
    LIMIT GREATEST(0,LEAST(COALESCE(p_limit,0),100));
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS find_fragments_bm25(UUID, TEXT, INTEGER);
