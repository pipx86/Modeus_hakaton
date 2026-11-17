package analysis

import (
	"reflect"
	"testing"
)

func TestExtractSkillsExactMatchSingleWord(t *testing.T) {
	cat := NewCatalog([]string{"python", "sql", "docker"})
	texts := []string{
		"Senior Python developer with SQL experience",
		"Docker and Kubernetes orchestration",
	}
	res := ExtractSkills(texts, cat)

	if res.Freq["python"] != 1 {
		t.Fatalf("python ожидали 1, получили %d", res.Freq["python"])
	}
	if res.Freq["sql"] != 1 {
		t.Fatalf("sql ожидали 1, получили %d", res.Freq["sql"])
	}
	if res.Freq["docker"] != 1 {
		t.Fatalf("docker ожидали 1, получили %d", res.Freq["docker"])
	}
	if len(res.PerVacancy) != 2 {
		t.Fatalf("ожидали 2 записи per-vacancy, получили %d", len(res.PerVacancy))
	}
}

func TestExtractSkillsMultiWordPhrase(t *testing.T) {
	cat := NewCatalog([]string{"machine learning", "deep learning"})
	texts := []string{
		"Experience in machine learning models",
		"No AI here",
	}
	res := ExtractSkills(texts, cat)

	if res.Freq["machine learning"] != 1 {
		t.Fatalf("multi-word phrase должен был найтись: freq=%d", res.Freq["machine learning"])
	}
	if res.Freq["deep learning"] != 0 {
		t.Fatalf("absent phrase не должна была появиться: freq=%d", res.Freq["deep learning"])
	}
}

func TestExtractSkillsAvoidsSubstringFalsePositive(t *testing.T) {
	// Ключевой кейс из Python: "sql" НЕ должен матчить внутри "mysql"/"postgresql".
	cat := NewCatalog([]string{"sql"})
	texts := []string{
		"MySQL database administration",
		"PostgreSQL replication setup",
		"Plain SQL queries only",
	}
	res := ExtractSkills(texts, cat)

	if res.Freq["sql"] != 1 {
		t.Fatalf("sql должен быть найден только в третьей вакансии (exact token), получили %d", res.Freq["sql"])
	}
}

func TestExtractSkillsWeightsAreNormalizedTF(t *testing.T) {
	cat := NewCatalog([]string{"python", "java"})
	texts := []string{
		"python",
		"java",
	}
	res := ExtractSkills(texts, cat)

	if diff := absDiff(res.WeightsRaw["python"], 0.5); diff > 1e-9 {
		t.Fatalf("weight(python) ожидали 0.5, получили %.4f", res.WeightsRaw["python"])
	}
	if diff := absDiff(res.WeightsRaw["java"], 0.5); diff > 1e-9 {
		t.Fatalf("weight(java) ожидали 0.5, получили %.4f", res.WeightsRaw["java"])
	}
}

func TestExtractSkillsEmptyInput(t *testing.T) {
	cat := NewCatalog([]string{"python"})
	res := ExtractSkills(nil, cat)
	if len(res.Freq) != 0 || len(res.PerVacancy) != 0 {
		t.Fatalf("пустой ввод обязан дать пустой результат: %+v", res)
	}
}

func TestExtractSkillsEmptyCatalog(t *testing.T) {
	cat := NewCatalog(nil)
	texts := []string{"anything goes here"}
	res := ExtractSkills(texts, cat)
	if len(res.Freq) != 0 {
		t.Fatalf("пустой каталог не должен находить ничего: %+v", res.Freq)
	}
}

func absDiff(a, b float64) float64 {
	d := a - b
	if d < 0 {
		return -d
	}
	return d
}

func TestExtractSkillsTaxonomyAliases(t *testing.T) {
	catalog := NewTaxonomyCatalog(
		[]string{
			"go",
			"golang",
			"postgresql",
			"postgres",
			"gitlab ci",
		},
		DefaultTaxonomy(),
	)

	result := ExtractSkills([]string{
		"Мы ищем Golang разработчика со знанием Postgres и GitLab CI",
	}, catalog)

	expected := map[string]int{
		"go":         1,
		"postgresql": 1,
		"ci/cd":      1,
	}

	if !reflect.DeepEqual(result.Freq, expected) {
		t.Errorf("ожидалось %v, получено %v", expected, result.Freq)
	}
}

func TestExtractSkillsCaseInsensitive(t *testing.T) {
	catalog := NewTaxonomyCatalog(
		[]string{"docker"},
		DefaultTaxonomy(),
	)

	result := ExtractSkills([]string{
		"Docker docker DOCKER",
	}, catalog)

	if result.Freq["docker"] != 1 {
		t.Errorf("ожидалась частота Docker = 1, получено %d", result.Freq["docker"])
	}
}

func TestExtractSkillsAvoidsDockerSubstringFalsePositive(t *testing.T) {
	catalog := NewTaxonomyCatalog(
		[]string{"docker"},
		DefaultTaxonomy(),
	)

	result := ExtractSkills([]string{
		"We use dockerized applications",
	}, catalog)

	if result.Freq["docker"] != 0 {
		t.Errorf("docker не должен определяться внутри dockerized, получено %d", result.Freq["docker"])
	}
}

func TestExtractSkillsAliasesAndWordBoundaries(t *testing.T) {
	catalog := NewTaxonomyCatalog(
		[]string{"go", "golang", "postgresql", "postgres"},
		DefaultTaxonomy(),
	)

	result := ExtractSkills([]string{
		"We use golang and postgres",
		"Our project uses golang-based architecture",
	}, catalog)

	expected := map[string]int{
		"go":         2,
		"postgresql": 1,
	}

	if !reflect.DeepEqual(result.Freq, expected) {
		t.Errorf("ожидалось %v, получено %v", expected, result.Freq)
	}
}
