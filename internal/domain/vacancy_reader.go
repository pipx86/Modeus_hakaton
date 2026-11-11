package domain

import "context"

type VacancyRecord struct {
	ID          string
	SourceID    string
	Title       string
	Description string
	SourceURL   string
}

type VacancyReader interface {
	ByProfession(ctx context.Context, profession string, limit int) ([]VacancyRecord, error)
}
