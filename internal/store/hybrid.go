package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgtype"
	"sort"
	"strings"
)

// SearchHybrid объединяет ранги векторного и лексического поиска методом RRF.
// Косинусные оценки и BM25 напрямую не складываются.
func (s *Store) SearchHybrid(ctx context.Context, run pgtype.UUID, query, model string, limit int, embed EmbeddingFunc) ([]SearchHit, string, error) {
	if limit < 1 || limit > 100 || strings.TrimSpace(query) == "" {
		return nil, "", errors.New("нужны запрос и limit от 1 до 100")
	}
	candidateLimit := limit * 3
	if candidateLimit > 100 {
		candidateLimit = 100
	}
	vector, mode, err := s.SearchFragments(ctx, run, query, model, candidateLimit, embed)
	if err != nil {
		return nil, mode, err
	}
	if mode == "bm25" {
		if len(vector) > limit {
			vector = vector[:limit]
		}
		return vector, mode, nil
	}
	lexical, _, err := s.SearchFragments(ctx, run, query, model, candidateLimit, nil)
	if err != nil {
		return nil, "hybrid", err
	}
	return fuseRanks(vector, lexical, limit), "hybrid", nil
}
func fuseRanks(a, b []SearchHit, limit int) []SearchHit {
	hits := map[pgtype.UUID]SearchHit{}
	for _, list := range [][]SearchHit{a, b} {
		seen := map[pgtype.UUID]bool{}
		for rank, hit := range list {
			if seen[hit.RefID] {
				continue
			}
			seen[hit.RefID] = true
			previous, ok := hits[hit.RefID]
			score := 1.0 / float64(60+rank+1)
			if ok {
				previous.Score += score
				hits[hit.RefID] = previous
			} else {
				hit.Score = score
				hits[hit.RefID] = hit
			}
		}
	}
	out := make([]SearchHit, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return string(out[i].RefID.Bytes[:]) < string(out[j].RefID.Bytes[:])
		}
		return out[i].Score > out[j].Score
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
