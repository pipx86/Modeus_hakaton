package store

import (
	"activity-descriptor/internal/domain"
	"context"
	"errors"
	"sort"
	"strings"
	"unicode"
)

type VacancyAdapter struct {
	store *Store
}

func NewVacancyAdapter(s *Store) *VacancyAdapter {
	return &VacancyAdapter{store: s}
}

func (a *VacancyAdapter) ByProfession(ctx context.Context, profession string, limit int) ([]domain.VacancyRecord, error) {
	if strings.TrimSpace(profession) == "" {
		return nil, errors.New("profession is required")
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	rows, err := a.store.pool.Query(ctx, `
		SELECT COALESCE(external_id::text,''), id::text, title, COALESCE(content,''), COALESCE(source_url,'')
		FROM sources
		WHERE provider IN ('hh', 'trudvsem') AND type = 'vacancy'
		  AND (title ILIKE $1 OR content ILIKE $1)
		ORDER BY updated_at DESC NULLS LAST
		LIMIT $2`, "%"+strings.TrimSpace(profession)+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out, err := scanVacancyRows(rows)
	if err != nil {
		return nil, err
	}
	if len(out) > 0 {
		return out, nil
	}

	// The exact phrase often misses hyphenated or word-order variants
	// ("Backend-разработчик (junior)" vs "Junior Backend-разработчик").
	// Fall back to recent candidates ranked by token overlap.
	return a.byProfessionTokens(ctx, profession, limit)
}

func scanVacancyRows(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]domain.VacancyRecord, error) {
	var out []domain.VacancyRecord
	for rows.Next() {
		var rec domain.VacancyRecord
		if err := rows.Scan(&rec.ID, &rec.SourceID, &rec.Title, &rec.Description, &rec.SourceURL); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (a *VacancyAdapter) byProfessionTokens(ctx context.Context, profession string, limit int) ([]domain.VacancyRecord, error) {
	tokens := professionTokens(profession)
	if len(tokens) == 0 {
		return nil, nil
	}
	rows, err := a.store.pool.Query(ctx, `
		SELECT COALESCE(external_id::text,''), id::text, title, COALESCE(content,''), COALESCE(source_url,'')
		FROM sources
		WHERE provider IN ('hh', 'trudvsem') AND type = 'vacancy'
		ORDER BY updated_at DESC NULLS LAST
		LIMIT 300`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates, err := scanVacancyRows(rows)
	if err != nil {
		return nil, err
	}
	type scored struct {
		record domain.VacancyRecord
		score  int
	}
	ranked := make([]scored, 0, len(candidates))
	for _, candidate := range candidates {
		title := strings.ToLower(candidate.Title)
		content := strings.ToLower(candidate.Description)
		score := 0
		for _, token := range tokens {
			if strings.Contains(title, token) {
				score += 3
			} else if strings.Contains(content, token) {
				score++
			}
		}
		if score > 0 {
			ranked = append(ranked, scored{record: candidate, score: score})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]domain.VacancyRecord, 0, len(ranked))
	for _, item := range ranked {
		out = append(out, item.record)
	}
	return out, nil
}

func professionTokens(value string) []string {
	words := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := make(map[string]bool, len(words))
	out := make([]string, 0, len(words))
	for _, word := range words {
		if len([]rune(word)) < 2 {
			continue
		}
		if seen[word] {
			continue
		}
		seen[word] = true
		out = append(out, word)
	}
	return out
}
