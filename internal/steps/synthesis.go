package steps

import (
	"context"
	"fmt"

	"activity-descriptor/internal/analysis"
	"activity-descriptor/internal/domain"
)

type SynthesisStep struct{}

func (s *SynthesisStep) Name() domain.Stage {
	return domain.StageSynthesis
}

func (s *SynthesisStep) Execute(_ context.Context, _ *domain.Run) error {
	return fmt.Errorf("synthesis step requires accumulated state; use ExecuteWithData")
}

func (s *SynthesisStep) ExecuteWithData(ctx context.Context, run *domain.Run, vacancies []domain.VacancyRecord) error {
	if run == nil {
		return fmt.Errorf("run is nil")
	}

	if run.Data == nil {
		run.Data = make(map[string]any)
	}

	inputs := make([]analysis.VacancyInput, 0, len(vacancies))
	descs := make([]string, 0, len(vacancies))

	for i, v := range vacancies {
		skills := pickSkillsFromData(run, i)

		inputs = append(inputs, analysis.VacancyInput{
			Name:       v.Title,
			Skills:     skills,
			Experience: extractExperienceHint(v.Description),
		})

		descs = append(descs, v.Description)
	}

	result := analysis.AssignLevels(inputs, descs)

	levelMaps := make([]map[string]any, 0, len(result.Levels))

	for _, le := range result.Levels {
		entry := map[string]any{
			"skills":      le.Skills,
			"description": le.Description,
			"category":    le.Category,
		}

		if le.Category == "BackEnd" {
			entry["experience"] = string(le.Experience)
			entry["confidence"] = le.Confidence
		}

		levelMaps = append(levelMaps, entry)
	}

	run.Data["level_data"] = levelMaps
	run.Data["level_skills"] = result.Skills

	return nil
}

func pickSkillsFromData(run *domain.Run, index int) []string {
	raw, ok := run.Data["per_vacancy_skills"]
	if !ok {
		return nil
	}

	perVac, typedOk := raw.([][]string)
	if !typedOk || index >= len(perVac) {
		return nil
	}

	return perVac[index]
}

func extractExperienceHint(description string) string {
	d := toLowerASCII(description)

	switch {
	case contains(d, "less than 1 year", "no experience", "entry level", "стажёр", "без опыта"):
		return "junior"
	case contains(d, "1-3 years", "3-6 years", "mid-level"):
		return "middle"
	case contains(d, "6+ years", "10+ years", "lead", "principal", "архитектур"):
		return "senior"
	}

	return ""
}

func toLowerASCII(s string) string {
	b := []byte(s)

	for i := range b {
		if 'A' <= b[i] && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}

	return string(b)
}

func contains(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && indexOf(s, sub) >= 0 {
			return true
		}
	}

	return false
}

func indexOf(haystack, needle string) int {
	n := len(needle)

	if n == 0 || len(haystack) < n {
		return -1
	}

	for i := 0; i+n <= len(haystack); i++ {
		match := true

		for j := 0; j < n; j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}

		if match {
			return i
		}
	}

	return -1
}
