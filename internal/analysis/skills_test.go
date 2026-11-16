package analysis

import (
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

func TestExtractSkillsCaseInsensitive(t *testing.T) {
	cat := NewCatalog([]string{"PYTHON"}) // каталог хранит lowercase после NewCatalog
	texts := []string{"Python Developer"}
	res := ExtractSkills(texts, cat)
	if res.Freq["python"] != 1 {
		t.Fatalf("case-insensitive match сломан: %v", res.Freq)
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
