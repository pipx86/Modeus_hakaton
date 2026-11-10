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
	Levels []LevelEntry
	Skills [][]string
}

type LevelEntry struct {
	Skills      []string
	Description string
	Experience  ExperienceLevel
}

func AssignLevels(vacancies []VacancyInput, descriptions []string) LevelResult {
	var levels []LevelEntry
	var skills [][]string

	for i, v := range vacancies {
		if len(v.Skills) == 0 {
			continue
		}

		exp := classifyExperience(v.Experience)

		if exp == LevelMiddle {
			nameLower := strings.ToLower(v.Name)
			switch {
			case containsAny(nameLower, "junior", "младший", "стажёр", "intern"):
				exp = LevelJunior
			case containsAny(nameLower, "senior", "старший", "ведущий"):
				exp = LevelSenior
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
		})
		skills = append(skills, v.Skills)
	}

	return LevelResult{Levels: levels, Skills: skills}
}

func normalizeExp(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "ё", "е") // стажёр → стажер
	return s
}

func classifyExperience(expStr string) ExperienceLevel {
	norm := normalizeExp(expStr)
	if norm == "" {
		return LevelMiddle
	}

	switch {
	case containsAny(norm, "noexperience", "lessthan1", "entrylevel", "junior", "стажер", "безопыта", "нетопыта"):
		return LevelJunior
	case containsAny(norm, "between6and10", "over10", "morethan10", "senior", "lead", "principal", "более6", "архитектур"):
		return LevelSenior
	}
	return LevelMiddle
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
