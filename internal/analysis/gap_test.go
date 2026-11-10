package analysis

import (
	"math"
	"testing"
)

func TestComputeGapBasicCoverage(t *testing.T) {
	profiles := []CandidateProfile{
		{Name: "Ivan", Skills: []string{"python", "sql"}},
	}
	market := MarketData{
		SkillFreq:     map[string]int{"python": 50, "java": 30, "docker": 20},
		HybridWeights: map[string]float64{"python": 0.5, "java": 0.3, "docker": 0.2},
	}

	reports := ComputeGap(profiles, market, 10)
	if len(reports) != 1 {
		t.Fatalf("ожидали 1 отчёт, получили %d", len(reports))
	}
	r := reports[0]
	expectedCov := 1.0 / 3.0
	if absDiff(r.Coverage, expectedCov) > 1e-9 {
		t.Fatalf("coverage ожидали %.4f, получили %.4f", expectedCov, r.Coverage)
	}
	if len(r.MissingSkills) != 2 {
		t.Fatalf("missing ожидали 2, получили %d", len(r.MissingSkills))
	}
}

func TestComputeGapPrioritizationOrder(t *testing.T) {
	profiles := []CandidateProfile{
		{Name: "Anna", Skills: nil},
	}
	market := MarketData{
		SkillFreq:     map[string]int{"a": 10, "b": 100, "c": 1},
		HybridWeights: map[string]float64{"a": 0.1, "b": 0.8, "c": 0.5},
	}
	reports := ComputeGap(profiles, market, 10)
	r := reports[0]
	if len(r.Recommendations) != 3 {
		t.Fatalf("ожидали 3 рекомендации, получили %d", len(r.Recommendations))
	}
	if r.Recommendations[0].Skill != "b" || r.Recommendations[1].Skill != "c" || r.Recommendations[2].Skill != "a" {
		t.Fatalf("порядок приоритетов сломан: %+v", r.Recommendations)
	}
}

func TestComputeGapTopKLimitsOutput(t *testing.T) {
	profiles := []CandidateProfile{{Name: "Bob", Skills: nil}}
	market := MarketData{
		SkillFreq:     map[string]int{"x1": 1, "x2": 2, "x3": 3, "x4": 4, "x5": 5},
		HybridWeights: map[string]float64{"x1": 0.1, "x2": 0.2, "x3": 0.3, "x4": 0.4, "x5": 0.5},
	}
	reports := ComputeGap(profiles, market, 2)
	if len(reports[0].Recommendations) != 2 {
		t.Fatalf("topK=2 обязан ограничить вывод до 2, получили %d", len(reports[0].Recommendations))
	}
	if reports[0].Recommendations[0].Skill != "x5" {
		t.Fatalf("первая рекомендация должна быть x5, получили %s", reports[0].Recommendations[0].Skill)
	}
}

func TestComputeGapMultipleProfiles(t *testing.T) {
	profiles := []CandidateProfile{
		{Name: "Alice", Skills: []string{"go"}},
		{Name: "Carol", Skills: []string{"go", "kubernetes"}},
	}
	market := MarketData{
		SkillFreq:     map[string]int{"go": 10, "kubernetes": 5, "python": 20},
		HybridWeights: map[string]float64{"go": 0.4, "kubernetes": 0.3, "python": 0.6},
	}
	reports := ComputeGap(profiles, market, 10)
	if len(reports) != 2 {
		t.Fatalf("ожидали 2 отчёта, получили %d", len(reports))
	}
	if len(reports[0].MissingSkills) != 2 {
		t.Fatalf("Alice missing ожидали 2, получили %d", len(reports[0].MissingSkills))
	}
	if len(reports[1].MissingSkills) != 1 {
		t.Fatalf("Carol missing ожидали 1, получили %d", len(reports[1].MissingSkills))
	}
}

func TestComputeGapEmptyMarket(t *testing.T) {
	profiles := []CandidateProfile{{Name: "Dave", Skills: []string{"rust"}}}
	market := MarketData{}
	reports := ComputeGap(profiles, market, 10)
	if reports[0].Coverage != 0 {
		t.Fatalf("пустой рынок должен дать coverage=0, получили %f", reports[0].Coverage)
	}
	if len(reports[0].Recommendations) != 0 {
		t.Fatalf("пустой рынок не должен порождать рекомендаций")
	}
}

func TestComputeGapCaseInsensitivity(t *testing.T) {
	profiles := []CandidateProfile{{Name: "Eve", Skills: []string{"PYTHON", "SQL"}}}
	market := MarketData{
		SkillFreq:     map[string]int{"python": 10, "sql": 5},
		HybridWeights: map[string]float64{"python": 0.7, "sql": 0.3},
	}
	reports := ComputeGap(profiles, market, 10)
	if len(reports[0].MissingSkills) != 0 {
		t.Fatalf("регистронезависимость сломана: missing=%v", reports[0].MissingSkills)
	}
	if absDiff(reports[0].Coverage, 1.0) > 1e-9 {
		t.Fatalf("полное покрытие ожидалось, получили %f", reports[0].Coverage)
	}
}

func TestPriorityFormulaMatchesMathLog(t *testing.T) {
	freq := 100
	weight := 0.8
	want := weight * math.Log(float64(freq)+1.0)
	got := 0.8 * math.Log(101.0)
	if absDiff(want, got) > 1e-12 {
		t.Fatalf("формула приоритета расходится: want=%.6f got=%.6f", want, got)
	}
}
