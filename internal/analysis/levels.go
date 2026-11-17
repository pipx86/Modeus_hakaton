package analysis

import (
	"strings"
)

type ExperienceLevel string

const (
	LevelJunior ExperienceLevel = "junior"
	LevelMiddle ExperienceLevel = "middle"
	LevelSenior ExperienceLevel = "senior"
)

type VacancyInput struct {
	Name       string
	Skills     []string
	Experience string
}

type LevelResult struct {
	Levels   []LevelEntry
	Skills   [][]string
	Clusters map[string][]string
}

type LevelEntry struct {
	Skills      []string
	Description string
	Experience  ExperienceLevel
	Category    string
	Confidence  float64
}

func AssignLevels(vacancies []VacancyInput, descriptions []string) LevelResult {
	var levels []LevelEntry
	var skills [][]string
	clusters := make(map[string][]string)

	for i, v := range vacancies {
		if len(v.Skills) == 0 {
			continue
		}

		category := categorizeSkills(v.Name, v.Skills)

		var exp ExperienceLevel
		confidence := 0.0

		if category == "BackEnd" {
			exp = classifyExperience(v.Experience + " " + v.Name)

			confidence = 0.6
			levelText := strings.ToLower(v.Experience + " " + v.Name)

			if containsAny(levelText, "junior", "no experience", "intern", "senior", "lead", "architect") {
				confidence = 0.9
			}
		}

		for _, sk := range v.Skills {
			skLower := strings.ToLower(sk)

			if cat := getCategoryForSkill(skLower); cat != "" {
				clusters[cat] = append(clusters[cat], sk)
			}
		}

		desc := ""
		if i < len(descriptions) {
			desc = descriptions[i]
		}

		levels = append(levels, LevelEntry{
			Skills:      v.Skills,
			Description: desc,
			Experience:  exp,
			Category:    category,
			Confidence:  confidence,
		})

		skills = append(skills, v.Skills)
	}

	return LevelResult{
		Levels:   levels,
		Skills:   skills,
		Clusters: clusters,
	}
}

func categorizeSkills(name string, skills []string) string {
	lowerName := strings.ToLower(name)

	if containsAny(lowerName, "devops", "sre", "site reliability", "девопс") {
		return "DevOps"
	}

	if containsAny(lowerName, "data analyst", "data analytics", "data scientist", "аналитик данных") {
		return "Data Analyst"
	}

	if containsAny(lowerName, "backend", "back-end", "back end", "бэкенд") {
		return "BackEnd"
	}

	categories := []string{"BackEnd", "DevOps", "Data Analyst"}
	counts := make(map[string]int)

	for _, skill := range skills {
		category := getCategoryForSkill(strings.ToLower(skill))

		if category != "" {
			counts[category]++
		}
	}

	maxCategory := ""
	maxCount := 0

	for _, category := range categories {
		if counts[category] > maxCount {
			maxCount = counts[category]
			maxCategory = category
		}
	}

	return maxCategory
}

func getCategoryForSkill(skill string) string {
	devopsKeywords := []string{
		"docker", "kubernetes", "terraform", "ansible",
		"jenkins", "ci/cd", "aws", "azure", "gcp",
		"linux", "bash", "gitlab-ci", "prometheus",
		"grafana", "nginx", "helm",
	}

	dataKeywords := []string{
		"machine learning", "deep learning", "nlp",
		"data analysis", "etl", "spark", "hadoop",
		"pandas", "numpy", "scikit-learn",
		"statistics", "data visualization",
		"tableau", "power bi", "matplotlib",
	}

	backendKeywords := []string{
		"python", "java", "go", "golang", "node.js",
		"django", "flask", "spring", "sql",
		"database", "api", "rest", "grpc",
		"postgresql", "mysql", "redis",
	}

	if containsAny(skill, devopsKeywords...) {
		return "DevOps"
	}

	if containsAny(skill, dataKeywords...) {
		return "Data Analyst"
	}

	if containsAny(skill, backendKeywords...) {
		return "BackEnd"
	}

	return ""
}

func classifyExperience(expStr string) ExperienceLevel {
	lower := strings.ToLower(strings.TrimSpace(expStr))

	if lower == "" {
		return LevelMiddle
	}

	norm := normalizeExp(lower)

	switch {
	case containsAny(norm, "less1", "noexperience", "junior", "intern", "стажер", "безопыта"):
		return LevelJunior

	case containsAny(norm, "between6and10", "over10", "morethan10", "senior", "lead", "principal", "architect", "5+years", "morethan5"):
		return LevelSenior
	}

	switch {
	case containsAny(lower, "junior", "no experience", "стажер", "intern"):
		return LevelJunior

	case containsAny(lower, "senior", "старший", "ведущий", "более 6", "5+ years", "lead", "architect"):
		return LevelSenior
	}

	return LevelMiddle
}

func normalizeExp(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "ё", "е")

	return s
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}

	return false
}
