package steps

import (
	"context"
	"fmt"
	"testing"

	"activity-descriptor/internal/domain"
)

func TestAnalysisStep(t *testing.T) {
	vacancies := []domain.VacancyRecord{
		{Description: "Go developer. Golang, Git, Linux, Docker, PostgreSQL"},
		{Description: "Python developer. Python, Django, Postgres, Redis"},
		{Description: "DevOps engineer. Linux, Docker, Kubernetes, GitLab CI"},
	}

	run := &domain.Run{}
	step := &AnalysisStep{}

	err := step.ExecuteWithData(context.Background(), run, vacancies)
	if err != nil {
		t.Fatal(err)
	}

	fmt.Println("\n========== РЕЗУЛЬТАТ АНАЛИЗА ==========")

	fmt.Println("\n1. ЧАСТОТА НАВЫКОВ:")
	fmt.Printf("%v\n", run.Data["skill_freq"])

	fmt.Println("\n2. ВЕСА НАВЫКОВ:")
	fmt.Printf("%v\n", run.Data["hybrid_weights_raw"])

	fmt.Println("\n3. НАВЫКИ ПО ВАКАНСИЯМ:")
	fmt.Printf("%v\n", run.Data["per_vacancy_skills"])

	fmt.Println("\n4. ИСХОДНЫЕ ВАКАНСИИ:")
	fmt.Printf("Количество: %d\n", len(vacancies))

	fmt.Println("\n=======================================")

	for _, key := range []string{
		"skill_freq",
		"hybrid_weights_raw",
		"per_vacancy_skills",
		"source_vacancies",
	} {
		if _, ok := run.Data[key]; !ok {
			t.Errorf("Результат %s отсутствует", key)
		}
	}
}

func TestAnalysisStepEmptyDescription(t *testing.T) {
	vacancies := []domain.VacancyRecord{
		{Description: ""},
	}

	run := &domain.Run{}
	step := &AnalysisStep{}

	err := step.ExecuteWithData(context.Background(), run, vacancies)
	if err != nil {
		t.Fatal(err)
	}

	freq, ok := run.Data["skill_freq"].(map[string]int)
	if !ok {
		t.Fatalf("skill_freq имеет неожиданный тип: %T", run.Data["skill_freq"])
	}

	if len(freq) != 0 {
		t.Errorf("ожидались пустые навыки, получено: %v", freq)
	}
}

func TestAnalysisStepRepeatedGit(t *testing.T) {
	vacancies := []domain.VacancyRecord{
		{Description: "Git, Git, Git, Git"},
	}

	run := &domain.Run{}
	step := &AnalysisStep{}

	err := step.ExecuteWithData(context.Background(), run, vacancies)
	if err != nil {
		t.Fatal(err)
	}

	freq := run.Data["skill_freq"].(map[string]int)

	if freq["git"] != 1 {
		t.Errorf("ожидалась частота Git = 1, получено: %d", freq["git"])
	}
}

func TestAnalysisStepUnknownSkill(t *testing.T) {
	vacancies := []domain.VacancyRecord{
		{Description: "Go, SuperTechXYZ"},
	}

	run := &domain.Run{}
	step := &AnalysisStep{}

	err := step.ExecuteWithData(context.Background(), run, vacancies)
	if err != nil {
		t.Fatal(err)
	}

	freq := run.Data["skill_freq"].(map[string]int)

	if freq["go"] != 1 {
		t.Errorf("Go должен быть найден, получено: %d", freq["go"])
	}

	if _, exists := freq["supertechxyz"]; exists {
		t.Error("неизвестная технология не должна определяться")
	}
}

func TestAnalysisStepGitInMultipleVacancies(t *testing.T) {
	vacancies := []domain.VacancyRecord{
		{Description: "Go developer, Git"},
		{Description: "Python developer, Git"},
		{Description: "Linux administrator"},
	}

	run := &domain.Run{}
	step := &AnalysisStep{}

	err := step.ExecuteWithData(context.Background(), run, vacancies)
	if err != nil {
		t.Fatal(err)
	}

	freq := run.Data["skill_freq"].(map[string]int)

	if freq["git"] != 2 {
		t.Errorf("ожидалась частота Git = 2, получено: %d", freq["git"])
	}
}
