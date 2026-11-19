package main

import (
	"context"

	"activity-descriptor/internal/api"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/learning"
	"activity-descriptor/internal/llm"
)

// learningPlannerAdapter builds a beginner-oriented plan from the same
// profession context that powers the interview.
type learningPlannerAdapter struct {
	store     *api.PGStore
	vacancies domain.VacancyReader
	client    llm.ChatClient
}

func newLearningPlanner(store *api.PGStore, vacancies domain.VacancyReader, client llm.ChatClient) api.LearningPlanner {
	return &learningPlannerAdapter{store: store, vacancies: vacancies, client: client}
}

func (p *learningPlannerAdapter) Plan(ctx context.Context, profession string) (learning.Plan, error) {
	sources := professionSources(p.store, p.vacancies, profession)
	return learning.Generate(ctx, p.client, profession, sources)
}
