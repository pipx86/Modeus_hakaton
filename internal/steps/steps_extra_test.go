package steps

import (
	"context"
	"errors"
	"testing"

	"activity-descriptor/internal/analysis"
	"activity-descriptor/internal/domain"
)

func TestAnalysisStep(t *testing.T) {
	step := &AnalysisStep{}
	if err := step.Execute(context.Background(), &domain.Run{}); err == nil {
		t.Fatal("Execute should require data")
	}
	if err := step.ExecuteWithData(context.Background(), nil, nil); err == nil {
		t.Fatal("nil run should fail")
	}

	run := &domain.Run{ID: "run-analysis"}
	vacancies := []domain.VacancyRecord{
		{Title: "Backend", Description: "Go, SQL, Docker"},
		{Title: "Analyst", Description: "SQL and Python"},
	}
	if err := step.ExecuteWithData(context.Background(), run, vacancies); err != nil {
		t.Fatalf("analysis: %v", err)
	}
	if len(run.Data["skill_freq"].(map[string]int)) == 0 {
		t.Fatalf("skill_freq = %#v", run.Data["skill_freq"])
	}
	if len(run.Data["source_vacancies"].([]domain.VacancyRecord)) != 2 {
		t.Fatal("source vacancies not stored")
	}
	if len(defaultITCatalog().Skills()) == 0 {
		t.Fatal("default catalog is empty")
	}
}

func TestInterviewStep(t *testing.T) {
	step := &InterviewStep{}
	if err := step.Execute(context.Background(), &domain.Run{}); err == nil {
		t.Fatal("Execute should require data")
	}
	if err := step.ExecuteWithData(context.Background(), nil, nil); err != nil {
		t.Fatalf("ExecuteWithData: %v", err)
	}
	if step.Name() != domain.StageInterview {
		t.Fatalf("name = %s", step.Name())
	}
}

func TestSynthesisStep(t *testing.T) {
	step := &SynthesisStep{}
	if err := step.Execute(context.Background(), &domain.Run{}); err == nil {
		t.Fatal("Execute should require data")
	}
	if err := step.ExecuteWithData(context.Background(), nil, nil); err == nil {
		t.Fatal("nil run should fail")
	}

	run := &domain.Run{ID: "run-synthesis", Data: map[string]any{
		"per_vacancy_skills": [][]string{{"python", "sql"}, {"go", "docker"}},
	}}
	vacancies := []domain.VacancyRecord{
		{Title: "Junior", Description: "less than 1 year, Python, SQL"},
		{Title: "Senior", Description: "6+ years, Go, Docker"},
	}
	if err := step.ExecuteWithData(context.Background(), run, vacancies); err != nil {
		t.Fatalf("synthesis: %v", err)
	}
	levels, ok := run.Data["level_data"].([]map[string]any)
	if !ok || len(levels) != len(vacancies) {
		t.Fatalf("level_data = %#v", run.Data["level_data"])
	}
	if len(run.Data["level_skills"].([][]string)) == 0 {
		t.Fatal("level_skills is empty")
	}
}

func TestSynthesisHelpers(t *testing.T) {
	cases := map[string]string{
		"no experience required": "junior",
		"1-3 years of work":      "middle",
		"6+ years, lead role":    "senior",
		"some other text":        "",
	}
	for input, want := range cases {
		if got := extractExperienceHint(input); got != want {
			t.Fatalf("extractExperienceHint(%q) = %q, want %q", input, got, want)
		}
	}
	if !contains("hello world", "world") || contains("hello", "world") {
		t.Fatal("contains failed")
	}
	if indexOf("hello", "ll") != 2 || indexOf("hello", "z") != -1 {
		t.Fatal("indexOf failed")
	}
	if got := toLowerASCII("ABC"); got != "abc" {
		t.Fatalf("toLowerASCII = %q", got)
	}
}

func TestReviewStep(t *testing.T) {
	if err := (&ReviewStep{}).Execute(context.Background(), nil); err == nil {
		t.Fatal("nil run should fail")
	}

	store := &reviewStore{}
	step := &ReviewStep{Store: store, TopK: 2}
	run := &domain.Run{
		ID:         "run-review",
		Profession: "Backend developer",
		Data: map[string]any{
			"skill_freq":         map[string]int{"go": 2, "sql": 1},
			"hybrid_weights_raw": map[string]float64{"go": 1.5},
			"source_vacancies":   []domain.VacancyRecord{{ID: "vac-1", SourceID: "11111111-1111-4111-8111-111111111111", Title: "Backend", Description: "Go and SQL"}},
			"per_vacancy_skills": [][]string{{"go", "sql"}},
		},
	}
	if err := step.Execute(context.Background(), run); err != nil {
		t.Fatalf("review: %v", err)
	}
	if len(store.nodes) == 0 {
		t.Fatal("tree was not saved")
	}
	if len(run.Data["gap_reports"].([]analysis.GapReport)) == 0 {
		t.Fatal("gap reports missing")
	}

	existing := &ReviewStep{Store: &reviewStore{exists: true}}
	if err := existing.Execute(context.Background(), &domain.Run{ID: "run-review"}); err != nil {
		t.Fatalf("existing tree should short-circuit: %v", err)
	}
}

type reviewStore struct {
	nodes  []domain.Node
	exists bool
	err    error
}

func (s *reviewStore) SaveTree(_ context.Context, _ string, nodes []domain.Node) error {
	s.nodes = nodes
	return s.err
}

func (s *reviewStore) TreeExists(context.Context, string) (bool, error) {
	return s.exists, s.err
}

func TestReviewStepErrors(t *testing.T) {
	step := &ReviewStep{Store: &reviewStore{err: errors.New("boom")}}
	if err := step.Execute(context.Background(), &domain.Run{ID: "run-error"}); err == nil {
		t.Fatal("TreeExists error should propagate")
	}
	noData := &ReviewStep{}
	if err := noData.Execute(context.Background(), &domain.Run{ID: "run-no-data"}); err == nil {
		t.Fatal("missing run.Data should fail")
	}
}
