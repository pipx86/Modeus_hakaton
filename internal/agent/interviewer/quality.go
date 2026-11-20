package interviewer

import (
	"strings"
)

// AnswerQuality is a transparent rubric for one interview answer.
type AnswerQuality struct {
	Specificity int `json:"specificity"`
	Evidence    int `json:"evidence"`
	Ownership   int `json:"ownership"`
	Impact      int `json:"impact"`
	Total       int `json:"total"`
}

// ScoreAnswer scores an answer on four dimensions. The score is deliberately
// deterministic: it helps the interviewer ask a better follow-up and gives the
// specialist an explainable signal, not a final hiring decision.
func ScoreAnswer(text string) (AnswerQuality, []string) {
	normalized := strings.ToLower(strings.TrimSpace(text))
	words := strings.Fields(normalized)
	quality := AnswerQuality{}
	notes := make([]string, 0, 3)

	switch {
	case len(words) >= 30:
		quality.Specificity = 30
	case len(words) >= 15:
		quality.Specificity = 24
	case len(words) >= 8:
		quality.Specificity = 16
	case len(words) >= 4:
		quality.Specificity = 8
	default:
		notes = append(notes, "Ответ слишком короткий: нужен конкретный пример.")
	}

	for _, marker := range []string{"например", "потому", "сделал", "проверил", "настроил", "написал", "запустил", "сравнил"} {
		if strings.Contains(normalized, marker) {
			quality.Evidence += 8
		}
	}
	if quality.Evidence > 25 {
		quality.Evidence = 25
	}

	for _, marker := range []string{"я ", "мой ", "моя ", "сам ", "отвечал", "руководил"} {
		if strings.Contains(normalized, marker) {
			quality.Ownership += 8
		}
	}
	if quality.Ownership > 20 {
		quality.Ownership = 20
	}

	for _, marker := range []string{"%", "процент", "секунд", "минут", "час", "дней", "раз", "до ", "после ", "сократил", "увеличил", "снизил"} {
		if strings.Contains(normalized, marker) {
			quality.Impact += 8
		}
	}
	if quality.Impact > 25 {
		quality.Impact = 25
	}

	quality.Total = quality.Specificity + quality.Evidence + quality.Ownership + quality.Impact
	if quality.Total < 50 {
		notes = append(notes, "Не хватает проверяемого результата: спросите про метрику или итог.")
	}
	if quality.Total < 30 {
		notes = append(notes, "Ответ лучше раскрыть по схеме: ситуация, задача, действие, результат.")
	}
	return quality, notes
}

// Contradictions finds explicit denials of skills that are visible in the
// respondent's public GitHub profile.
func Contradictions(skills []string, answer string) []string {
	normalized := strings.ToLower(strings.TrimSpace(answer))
	if normalized == "" || len(skills) == 0 {
		return nil
	}
	patterns := []string{"не работал с ", "не использовал ", "не знаю ", "не знаком с ", "не умею "}
	var conflicts []string
	for _, skill := range skills {
		needle := strings.ToLower(strings.TrimSpace(skill))
		if needle == "" {
			continue
		}
		for _, pattern := range patterns {
			if strings.Contains(normalized, pattern+needle) {
				conflicts = append(conflicts, skill)
				break
			}
		}
		if containsSkillDenial(normalized, needle) {
			conflicts = append(conflicts, skill)
		}
	}
	return uniqueStrings(conflicts)
}

func containsSkillDenial(answer, skill string) bool {
	for _, sentence := range strings.FieldsFunc(answer, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == ';' || r == '\n'
	}) {
		if strings.Contains(sentence, skill) &&
			(strings.Contains(sentence, "не ") || strings.Contains(sentence, "нет ")) {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
