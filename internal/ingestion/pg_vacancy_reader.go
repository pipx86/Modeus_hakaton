package ingestion

import (
	"context"
	"errors"
	"strings"

	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PGVacancyReader читает вакансии из таблицы sources (provider='hh', type='vacancy').
type PGVacancyReader struct {
	pool *pgxpool.Pool
}

func NewPGVacancyReader(pool *pgxpool.Pool) *PGVacancyReader {
	return &PGVacancyReader{pool: pool}
}

// ByProfession ищет вакансии по заголовку/описанию (ILIKE %profession%) с лимитом.
func (r *PGVacancyReader) ByProfession(ctx context.Context, profession string, limit int) ([]domain.VacancyRecord, error) {
	if strings.TrimSpace(profession) == "" {
		return nil, errors.New("profession is required")
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	rows, err := r.pool.Query(ctx, `
		SELECT external_id, title, COALESCE(content,''), COALESCE(source_url,'')
		FROM sources
		WHERE provider = 'hh' AND type = 'vacancy'
		  AND (title ILIKE $1 OR content ILIKE $1)
		ORDER BY updated_at DESC NULLS LAST
		LIMIT $2`, "%"+strings.TrimSpace(profession)+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.VacancyRecord
	for rows.Next() {
		var rec domain.VacancyRecord
		if err := rows.Scan(&rec.ID, &rec.Title, &rec.Description, &rec.SourceURL); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
