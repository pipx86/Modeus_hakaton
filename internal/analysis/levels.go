package analysis

import "strings"

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
	var allSkills [][]string
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
		for _, skill := range v.Skills {
			for _, cat := range categoriesForSkill(skill) {
				clusters[cat] = appendUnique(clusters[cat], skill)
			}
		}
		desc := ""
		if i < len(descriptions) {
			desc = descriptions[i]
		}
		levels = append(levels, LevelEntry{Skills: v.Skills, Description: desc, Experience: exp, Category: category, Confidence: confidence})
		allSkills = append(allSkills, v.Skills)
	}
	return LevelResult{Levels: levels, Skills: allSkills, Clusters: clusters}
}

func categorizeSkills(name string, skills []string) string {
	lowerName := strings.ToLower(name)
	switch {
	case containsAny(lowerName, "devops", "sre", "site reliability", "девопс"):
		return "DevOps"
	case containsAny(lowerName, "data analyst", "data analytics", "data scientist", "аналитик данных"):
		return "Data Analyst"
	case containsAny(lowerName, "backend", "back-end", "back end", "бэкенд"):
		return "BackEnd"
	}
	counts := map[string]int{}
	for _, skill := range skills {
		for _, category := range categoriesForSkill(skill) {
			counts[category]++
		}
	}
	order := []string{"BackEnd", "DevOps", "Data Analyst", "Machine Learning", "Frontend", "System Administration", "Cloud"}
	best, bestCount := "", 0
	for _, category := range order {
		if counts[category] > bestCount {
			best, bestCount = category, counts[category]
		}
	}
	return best
}

func categoriesForSkill(skill string) []string {
	categories := DefaultTaxonomy().Categories(skill)
	if len(categories) > 0 {
		return categories
	}
	return []string{getCategoryForSkill(skill)}
}

func getCategoryForSkill(skill string) string {
	categories := categoriesForSkillLegacy(skill)
	if len(categories) > 0 {
		return categories[0]
	}
	return ""
}

func categoriesForSkillLegacy(skill string) []string {
	s := strings.ToLower(skill)
	if containsAny(s, "docker", "kubernetes", "terraform", "ansible", "jenkins", "ci/cd", "aws", "azure", "gcp", "linux", "bash", "prometheus", "grafana", "nginx", "helm") {
		return []string{"DevOps"}
	}
	if containsAny(s, "machine learning", "deep learning", "nlp", "pandas", "numpy", "scikit-learn", "tableau", "power bi", "matplotlib", "data analysis", "etl", "spark", "hadoop") {
		return []string{"Data Analyst"}
	}
	if containsAny(s, "python", "java", "go", "golang", "node.js", "django", "flask", "spring", "sql", "database", "api", "rest", "grpc", "postgresql", "mysql", "redis") {
		return []string{"BackEnd"}
	}
	return nil
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

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}
