package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// EmbeddingFunc строит вектор текста; реализация клиента модели подключается извне.
type EmbeddingFunc func(context.Context, string) ([]float32, error)

// SearchHit содержит фрагмент источника и оценку выбранного способа поиска.
type SearchHit struct {
	RefID    pgtype.UUID `json:"ref_id"`
	SourceID pgtype.UUID `json:"source_id"`
	Quote    string      `json:"quote"`
	Score    float64     `json:"score"`
}

// SearchFragments использует векторный поиск или BM25 при ошибке построения вектора.
// Требуется необязательный пакет миграций поиска; права доступа проверяет вызывающая сторона.
func (s *Store) SearchFragments(ctx context.Context, runID pgtype.UUID, query, model string, limit int, embed EmbeddingFunc) ([]SearchHit, string, error) {
	if s == nil || s.pool == nil {
		return nil, "", errors.New("не задано подключение к базе")
	}
	if !runID.Valid || strings.TrimSpace(query) == "" || limit < 1 || limit > 100 {
		return nil, "", errors.New("нужны исследование, текст запроса и количество от 1 до 100")
	}
	if embed != nil && strings.TrimSpace(model) == "" {
		return nil, "", errors.New("для векторного поиска нужно имя модели")
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	mode := "bm25"
	sql := `SELECT ref_id, source_id, quote, score FROM find_fragments_bm25($1,$2,$3)`
	args := []any{runID, query, limit}
	if embed != nil {
		vector, err := embed(ctx, query)
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		if err == nil {
			if value, ok := formatVector(vector); ok {
				mode = "vector"
				sql = `SELECT ref_id, source_id, quote, score FROM find_relevant_fragments($1,$2::vector,$3,$4)`
				args = []any{runID, value, model, limit}
			}
		}
	}
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, mode, fmt.Errorf("поиск фрагментов: %w", err)
	}
	defer rows.Close()
	hits := make([]SearchHit, 0)
	for rows.Next() {
		var hit SearchHit
		if err := rows.Scan(&hit.RefID, &hit.SourceID, &hit.Quote, &hit.Score); err != nil {
			return nil, mode, fmt.Errorf("прочитать результат поиска: %w", err)
		}
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, mode, fmt.Errorf("прочитать результаты поиска: %w", err)
	}
	return hits, mode, nil
}
func formatVector(values []float32) (string, bool) {
	if len(values) != 768 {
		return "", false
	}
	var b strings.Builder
	b.WriteByte('[')
	nonzero := false
	for i, value := range values {
		n := float64(value)
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return "", false
		}
		if n != 0 {
			nonzero = true
		}
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(n, 'g', -1, 32))
	}
	b.WriteByte(']')
	if !nonzero {
		return "", false
	}
	return b.String(), true
}
