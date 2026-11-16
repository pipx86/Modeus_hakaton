package ingestion

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

var competencyLine = regexp.MustCompile(`(?i)^((?:УК|ОПК|ПК|ОК)-[0-9]+(?:\.[0-9]+)*)[.\s:—–-]*(.*)$`)

// ExtractEducationText извлекает компетенции из обычной текстовой структуры программы.
// Распознаются коды компетенций и разделы «Знать/Уметь/Владеть»; смысл не придумывается.
func ExtractEducationText(text, title string) (EducationProgram, error) {
	if result, err := ReadEducationText(text, title); err == nil {
		return result, nil
	}
	result := EducationProgram{Title: strings.TrimSpace(title)}
	current, kind := -1, ""
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if match := competencyLine.FindStringSubmatch(line); match != nil {
			result.Competencies = append(result.Competencies, ProgramCompetency{Code: strings.ToUpper(match[1]), Title: strings.TrimSpace(match[2])})
			current = len(result.Competencies) - 1
			kind = ""
			continue
		}
		if current < 0 {
			continue
		}
		lower := strings.ToLower(line)
		matched := false
		for _, heading := range []struct{ label, kind string }{{"знать", "knowledge"}, {"знания", "knowledge"}, {"уметь", "skill"}, {"умения", "skill"}, {"владеть", "ability"}, {"навыки", "ability"}} {
			if lower == heading.label || strings.HasPrefix(lower, heading.label+":") {
				kind = heading.kind
				value := ""
				if _, tail, ok := strings.Cut(line, ":"); ok {
					value = strings.TrimSpace(tail)
				} else {
					value = ""
				}
				if value != "" {
					result.Competencies[current].KSA = append(result.Competencies[current].KSA, ProgramKSA{Kind: kind, Title: value})
				}
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		c := &result.Competencies[current]
		if c.Title == "" && kind == "" {
			c.Title = line
			continue
		}
		if kind != "" {
			value := strings.TrimSpace(strings.TrimLeft(line, "-•— "))
			if value != "" {
				c.KSA = append(c.KSA, ProgramKSA{Kind: kind, Title: value})
			}
		}
	}
	if result.Title == "" || len(result.Competencies) == 0 {
		return result, errors.New("не найдены компетенции с кодами УК/ОПК/ПК/ОК; нужен структурированный JSON или документ с распознаваемыми разделами")
	}
	for _, c := range result.Competencies {
		if c.Title == "" || len(c.KSA) == 0 {
			return result, errors.New("у извлечённой компетенции нет названия или разделов Знать/Уметь/Владеть; требуется проверка документа")
		}
	}
	data, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	return ReadEducation(strings.NewReader(string(data)))
}
