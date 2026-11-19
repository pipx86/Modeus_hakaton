package chat

import (
	"strings"
	"testing"

	"activity-descriptor/internal/domain"
)

func TestFormatSourceContextIncludesMaterialsAndVacancies(t *testing.T) {
	text := FormatSourceContext([]domain.Source{
		{ID: "v1", Type: domain.SourceVacancy, Title: "Backend", Content: "Go and SQL"},
		{ID: "i1", Type: domain.SourceInterview, Title: "Interview", Content: "скрытый транскрипт"},
		{ID: "d1", Type: domain.SourceDocument, Title: "Методичка", Content: "Описание практики"},
	}, 6, 100)

	if !strings.Contains(text, "Вакансия") || !strings.Contains(text, "Backend") {
		t.Fatalf("vacancy missing: %q", text)
	}
	if !strings.Contains(text, "Материал") || !strings.Contains(text, "Методичка") {
		t.Fatalf("document missing: %q", text)
	}
	if strings.Contains(text, "скрытый транскрипт") {
		t.Fatalf("interview transcript must not be duplicated: %q", text)
	}
}

func TestFormatSourceContextLimitsAndTruncates(t *testing.T) {
	text := FormatSourceContext([]domain.Source{
		{ID: "v1", Type: domain.SourceVacancy, Title: "A", Content: strings.Repeat("я", 40)},
		{ID: "v2", Type: domain.SourceVacancy, Title: "B", Content: "second"},
	}, 1, 10)

	if strings.Contains(text, "second") {
		t.Fatalf("source limit ignored: %q", text)
	}
	if !strings.Contains(text, "…") {
		t.Fatalf("content was not truncated: %q", text)
	}
}
