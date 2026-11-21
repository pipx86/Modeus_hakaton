package ingestion

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

type vacancyLabel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (label *vacancyLabel) UnmarshalJSON(data []byte) error {
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte{'"'}) {
		if err := json.Unmarshal(data, &label.Name); err != nil {
			return err
		}
		label.ID = label.Name
		return nil
	}
	type plainLabel vacancyLabel
	return json.Unmarshal(data, (*plainLabel)(label))
}

type vacancyEmployer struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	LogoURLs map[string]string `json:"logo_urls"`
}

func (employer *vacancyEmployer) UnmarshalJSON(data []byte) error {
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte{'"'}) {
		return json.Unmarshal(data, &employer.Name)
	}
	type plainEmployer vacancyEmployer
	return json.Unmarshal(data, (*plainEmployer)(employer))
}

type vacancySalary struct {
	From     *int64 `json:"from,omitempty"`
	To       *int64 `json:"to,omitempty"`
	Currency string `json:"currency,omitempty"`
}

func (v Vacancy) marketDetails() ([]byte, error) {
	values := map[string]any{}
	labels := map[string]string{
		"area":          normalizeVacancyLabel(v.Area.Name),
		"employer_name": normalizeVacancyLabel(v.Employer.Name),
		"employer_id":   strings.TrimSpace(v.Employer.ID),
		"experience":    normalizeVacancyLabel(v.Experience.ID),
	}
	for key, value := range labels {
		if cleaned := strings.TrimSpace(value); cleaned != "" {
			values[key] = cleaned
		}
	}
	for _, size := range []string{"240", "90", "original"} {
		if logo := strings.TrimSpace(v.Employer.LogoURLs[size]); logo != "" {
			values["employer_logo"] = logo
			break
		}
	}
	parsed := v.ExtractedSkills
	if len(parsed) == 0 {
		parsed = v.RawData.ExtractedSkills
	}
	if skills := normalizeVacancySkillLabels(parsed); len(skills) > 0 {
		for i := range skills {
			skills[i] = strings.ToLower(skills[i])
		}
		values["parsed_skills"] = skills
	}
	if v.Salary.From != nil || v.Salary.To != nil {
		salary := v.Salary
		salary.Currency = strings.ToUpper(strings.TrimSpace(salary.Currency))
		values["salary"] = salary
	}
	var keySkillNames []string
	for _, skill := range v.KeySkills {
		keySkillNames = append(keySkillNames, skill.Name)
	}
	if skills := normalizeVacancySkillLabels(keySkillNames); len(skills) > 0 {
		values["key_skills"] = skills
	}
	snippet := struct {
		Requirement    string `json:"requirement"`
		Responsibility string `json:"responsibility"`
	}{
		Requirement:    normalizeVacancyLabel(v.Snippet.Requirement),
		Responsibility: normalizeVacancyLabel(v.Snippet.Responsibility),
	}
	if snippet.Requirement != "" || snippet.Responsibility != "" {
		values["snippet"] = snippet
	}
	if len(values) == 0 {
		return nil, nil
	}
	return json.Marshal(values)
}

func (v *Vacancy) setCSVMarketFields(get func(string) string) error {
	v.Area.Name = get("area")
	if v.Area.Name == "" {
		v.Area.Name = get("area_name")
	}
	v.Employer = vacancyEmployer{ID: get("employer_id"), Name: get("employer_name")}
	v.Experience.ID = get("experience")
	v.Salary.Currency = get("salary_currency")
	for _, bound := range []struct {
		key   string
		value **int64
	}{{"salary_from", &v.Salary.From}, {"salary_to", &v.Salary.To}} {
		if raw := strings.TrimSpace(get(bound.key)); raw != "" {
			amount, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || amount < 0 {
				return fmt.Errorf("%s: нужна целая неотрицательная сумма", bound.key)
			}
			*bound.value = &amount
		}
	}
	return nil
}

func ReadVacancyArchive(basicPath, detailedPath string) ([]Vacancy, error) {
	var result []Vacancy
	byID := make(map[string]int)
	for _, path := range []string{basicPath, detailedPath} {
		if path == "" {
			continue
		}
		items, err := ReadVacancyFile(path, "json")
		if err != nil {
			return nil, fmt.Errorf("архив вакансий %s: %w", filepath.Base(path), err)
		}
		for _, item := range items {
			id := strings.TrimSpace(item.ID)
			if pos, ok := byID[id]; ok {
				result[pos] = mergeArchiveVacancy(result[pos], item)
			} else {
				byID[id] = len(result)
				result = append(result, item)
			}
		}
	}
	return result, nil
}

func mergeArchiveVacancy(base, detail Vacancy) Vacancy {
	for _, pair := range []struct {
		dst *string
		src string
	}{
		{&base.Name, detail.Name}, {&base.URL, detail.URL}, {&base.Description, detail.Description},
		{&base.PublishedAt, detail.PublishedAt}, {&base.Area.Name, detail.Area.Name},
		{&base.Employer.ID, detail.Employer.ID}, {&base.Employer.Name, detail.Employer.Name},
		{&base.Experience.ID, detail.Experience.ID},
		{&base.Snippet.Requirement, detail.Snippet.Requirement}, {&base.Snippet.Responsibility, detail.Snippet.Responsibility},
	} {
		if strings.TrimSpace(pair.src) != "" {
			*pair.dst = pair.src
		}
	}
	if detail.Salary.From != nil || detail.Salary.To != nil {
		base.Salary = detail.Salary
	}
	if len(detail.KeySkills) > 0 {
		base.KeySkills = detail.KeySkills
	}
	if len(detail.Employer.LogoURLs) > 0 {
		base.Employer.LogoURLs = detail.Employer.LogoURLs
	}
	if len(detail.ExtractedSkills) > 0 {
		base.ExtractedSkills = detail.ExtractedSkills
	} else if len(detail.RawData.ExtractedSkills) > 0 {
		base.ExtractedSkills = detail.RawData.ExtractedSkills
	}
	return base
}
