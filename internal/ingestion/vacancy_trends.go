package ingestion

import (
	"context"
	"errors"
	"strings"
	"time"

	"activity-descriptor/internal/store/db"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type VacancyMonth struct {
	Month    string `json:"month"`
	Provider string `json:"provider"`
	Count    int64  `json:"count"`
}

type VacancyTrend struct {
	TitleQuery      string         `json:"title_query"`
	From            string         `json:"from"`
	Until           string         `json:"until_exclusive"`
	WithoutDate     int64          `json:"without_publication_date"`
	ObservedMonthly []VacancyMonth `json:"observed_monthly"`
}

// VacancyPublicationTrend считает сохранённые публикации, разбивая их по UTC-месяцам.
func VacancyPublicationTrend(ctx context.Context, pool *pgxpool.Pool, titleQuery string, from, until time.Time) (VacancyTrend, error) {
	titleQuery = strings.TrimSpace(titleQuery)
	if titleQuery == "" || !from.Before(until) || until.After(from.AddDate(2, 0, 0)) {
		return VacancyTrend{}, errors.New("нужны часть заголовка вакансии и период до 24 месяцев")
	}
	result := VacancyTrend{
		TitleQuery: titleQuery, From: from.UTC().Format(time.DateOnly), Until: until.UTC().Format(time.DateOnly),
		ObservedMonthly: []VacancyMonth{},
	}
	rows, err := db.New(pool).ListVacancyPublicationTrend(ctx, db.ListVacancyPublicationTrendParams{
		TitleQuery: titleQuery,
		FromDate:   pgtype.Timestamptz{Time: from, Valid: true},
		UntilDate:  pgtype.Timestamptz{Time: until, Valid: true},
	})
	if err != nil {
		return VacancyTrend{}, err
	}
	for _, row := range rows {
		if row.Month == "" {
			result.WithoutDate += row.VacancyCount
			continue
		}
		result.ObservedMonthly = append(result.ObservedMonthly, VacancyMonth{
			Month: row.Month, Provider: row.Provider, Count: row.VacancyCount,
		})
	}
	return result, nil
}
