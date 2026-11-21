package main

import (
	"activity-descriptor/internal/api"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/sourcecluster"
)

// newInterviewSourceContext loads admin materials and profession-matched
// vacancies for the interviewer. It intentionally never fails the chat: any
// lookup error simply leaves the session without extra context.
func newInterviewSourceContext(pgStore *api.PGStore, vacancies domain.VacancyReader) chat.SourceContextLoader {
	return func(_ string, profession string) string {
		return chat.FormatSourceContext(professionSources(pgStore, vacancies, profession), 50, 700)
	}
}

// professionSources returns admin materials and profession-matched vacancies.
// Lookup errors are ignored: callers can work with an empty context.
func professionSources(pgStore *api.PGStore, vacancies domain.VacancyReader, profession string) []domain.Source {
	sources := make([]domain.Source, 0, 13)
	if global, err := pgStore.ListGlobalSources(50); err == nil {
		sources = append(sources, global...)
	}
	return sourcecluster.Select("", sources, nil).Sources
}
