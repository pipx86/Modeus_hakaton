package steps

import (
	"context"
	"fmt"

	"activity-descriptor/internal/analysis"
	"activity-descriptor/internal/domain"
)

type AnalysisStep struct {
	Catalog *analysis.Catalog
}

func (a *AnalysisStep) Name() domain.Stage { return domain.StageAnalysis }

func (a *AnalysisStep) Execute(_ context.Context, _ *domain.Run) error {
	return fmt.Errorf("analysis step requires data; use ExecuteWithData")
}

func (a *AnalysisStep) ExecuteWithData(ctx context.Context, run *domain.Run, vacancies []domain.VacancyRecord) error {
	if run == nil {
		return fmt.Errorf("run is nil")
	}

	if run.Data == nil {
		run.Data = make(map[string]any)
	}

	texts := make([]string, 0, len(vacancies))
	for _, v := range vacancies {
		texts = append(texts, v.Description)
	}

	catalog := a.Catalog
	if catalog == nil {
		catalog = defaultITCatalog()
	}

	triple := analysis.ExtractSkills(texts, catalog)

	run.Data["skill_freq"] = triple.Freq
	run.Data["hybrid_weights_raw"] = triple.WeightsRaw
	run.Data["per_vacancy_skills"] = triple.PerVacancy
	run.Data["source_vacancies"] = vacancies

	return nil
}

func defaultITCatalog() *analysis.Catalog {
	return analysis.NewCatalog([]string{
		"python", "java", "javascript", "typescript", "go", "rust", "c++", "c#",
		"sql", "postgresql", "mysql", "mongodb", "redis", "elasticsearch",
		"docker", "kubernetes", "terraform", "ansible", "jenkins", "gitlab-ci",
		"aws", "azure", "gcp", "linux", "bash",
		"machine learning", "deep learning", "nlp", "computer vision",
		"data analysis", "etl", "airflow", "spark", "hadoop",
		"react", "angular", "vue", "node.js", "django", "flask", "spring",
		"kafka", "rabbitmq", "grpc", "rest api", "graphql",
		"unit testing", "integration testing", "ci/cd",
		"agile", "scrum", "kanban",
	})
}
