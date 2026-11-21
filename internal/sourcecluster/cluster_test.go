package sourcecluster

import (
	"testing"

	"activity-descriptor/internal/domain"
)

func TestSelectGroupsByProfession(t *testing.T) {
	sources := []domain.Source{
		{ID: "backend-vacancy", Type: domain.SourceVacancy, Title: "Senior Backend Engineer", Content: "Go, PostgreSQL, Docker"},
		{ID: "backend-standard", Type: domain.SourceStandard, Title: "Профстандарт Backend-разработчика", Content: "Разработка серверных сервисов"},
		{ID: "data-standard", Type: domain.SourceStandard, Title: "Профстандарт аналитика данных", Content: "SQL и дашборды"},
		{ID: "generic-doc", Type: domain.SourceDocument, Title: "Общая методика", Content: "Как проводить встречи"},
		{ID: "explicit-doc", Type: domain.SourceDocument, Title: "Материал команды", Content: "Внутренние договорённости"},
	}

	result := Select("Backend-разработчик (junior)", sources, map[string]bool{"explicit-doc": true})
	if result.Excluded != 2 {
		t.Fatalf("excluded = %d, want 2", result.Excluded)
	}
	ids := map[string]bool{}
	for _, source := range result.Sources {
		ids[source.ID] = true
	}
	if !ids["backend-vacancy"] || !ids["backend-standard"] || !ids["explicit-doc"] {
		t.Fatalf("selected sources = %#v", ids)
	}
	if ids["data-standard"] || ids["generic-doc"] {
		t.Fatalf("irrelevant sources leaked into cluster: %#v", ids)
	}
	if len(result.Clusters) != 1 || result.Clusters[0].Profession != "Backend-разработчик (junior)" {
		t.Fatalf("clusters = %+v", result.Clusters)
	}
}

func TestSelectRejectsContentOnlyCoincidence(t *testing.T) {
	sources := []domain.Source{
		{ID: "backend-vacancy", Type: domain.SourceVacancy, Title: "Backend-разработчик", Content: "Работа с данными и API"},
		{ID: "analyst-standard", Type: domain.SourceStandard, Title: "Профстандарт аналитика данных", Content: "SQL и дашборды"},
	}
	result := Select("Аналитик данных", sources, nil)
	if len(result.Sources) != 1 || result.Sources[0].ID != "analyst-standard" {
		t.Fatalf("sources = %+v", result.Sources)
	}
	if result.Excluded != 1 {
		t.Fatalf("excluded = %d, want 1", result.Excluded)
	}
}

func TestSelectKeepsEmptyProfessionPermissive(t *testing.T) {
	sources := []domain.Source{{ID: "s1", Title: "Anything", Content: "Text"}}
	result := Select("", sources, nil)
	if len(result.Sources) != 1 {
		t.Fatalf("sources = %+v", result.Sources)
	}
}
