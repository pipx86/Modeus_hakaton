package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"activity-descriptor/internal/domain"
)

// Карточка 7 плана MVP: исследование без привязанных источников должно
// анализировать материалы, которые уже есть в базе, а не только админские.

func TestSelectBaseSourcesWithoutSelectorReturnsNothing(t *testing.T) {
	if got := selectBaseSources(context.Background(), finalizerDeps{}, "Аналитик данных"); got != nil {
		t.Fatalf("без селектора ожидался nil, получено %d источников", len(got))
	}
}

func TestSelectBaseSourcesRequiresProfession(t *testing.T) {
	deps := finalizerDeps{baseSources: func(context.Context, string) []domain.Source {
		t.Fatal("селектор не должен вызываться без профессии")
		return nil
	}}
	if got := selectBaseSources(context.Background(), deps, "   "); got != nil {
		t.Fatalf("без профессии ожидался nil, получено %d источников", len(got))
	}
}

func TestSelectBaseSourcesDeduplicatesAndCaps(t *testing.T) {
	many := make([]domain.Source, 0, maxBaseSourceCount+4)
	for i := 0; i < maxBaseSourceCount+4; i++ {
		many = append(many, domain.Source{
			ID:      fmt.Sprintf("source-%d", i),
			Type:    domain.SourceVacancy,
			Title:   fmt.Sprintf("вакансия %d", i),
			Content: "текст",
		})
	}
	many = append(many, domain.Source{ID: "source-0", Title: "дубль", Content: "текст"})

	deps := finalizerDeps{baseSources: func(context.Context, string) []domain.Source { return many }}
	got := selectBaseSources(context.Background(), deps, "Аналитик данных")
	if len(got) != maxBaseSourceCount {
		t.Fatalf("ожидалось %d источников, получено %d", maxBaseSourceCount, len(got))
	}
	seen := map[string]bool{}
	for _, source := range got {
		if seen[source.ID] {
			t.Fatalf("источник %q продублирован", source.ID)
		}
		seen[source.ID] = true
	}
}

func TestSelectBaseSourcesSkipsEmptyRowsAndTruncatesContent(t *testing.T) {
	long := strings.Repeat("я", maxFinalizeSourceRunes+500)
	sources := []domain.Source{
		{ID: "", Title: "без идентификатора", Content: "текст"},
		{ID: "empty", Title: "   ", Content: "   "},
		{ID: "long", Title: "длинный документ", Content: long},
	}
	deps := finalizerDeps{baseSources: func(context.Context, string) []domain.Source { return sources }}

	got := selectBaseSources(context.Background(), deps, "Аналитик данных")
	if len(got) != 1 {
		t.Fatalf("ожидался 1 пригодный источник, получено %d", len(got))
	}
	if got[0].ID != "long" {
		t.Fatalf("ожидался источник long, получен %q", got[0].ID)
	}
	if runes := len([]rune(got[0].Content)); runes != maxFinalizeSourceRunes {
		t.Fatalf("контент должен обрезаться до %d рун, получено %d", maxFinalizeSourceRunes, runes)
	}
}
