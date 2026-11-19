package main

import (
	"context"
	"time"

	"activity-descriptor/internal/api"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
)

// newInterviewSourceContext loads admin materials and profession-matched
// vacancies for the interviewer. It intentionally never fails the chat: any
// lookup error simply leaves the session without extra context.
func newInterviewSourceContext(pgStore *api.PGStore, vacancies domain.VacancyReader) chat.SourceContextLoader {
	return func(_ string, profession string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		sources := make([]domain.Source, 0, 13)
		if global, err := pgStore.ListGlobalSources(5); err == nil {
			sources = append(sources, global...)
		}
		if vacancies != nil {
			if records, err := vacancies.ByProfession(ctx, profession, 8); err == nil {
				for _, record := range records {
					if record.SourceID == "" || record.Description == "" {
						continue
					}
					sources = append(sources, domain.Source{
						ID:        record.SourceID,
						Type:      domain.SourceVacancy,
						Title:     record.Title,
						Content:   record.Description,
						SourceURL: record.SourceURL,
					})
				}
			}
		}
		return chat.FormatSourceContext(sources, 8, 700)
	}
}
