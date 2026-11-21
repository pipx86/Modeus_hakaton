package api

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strings"

	"activity-descriptor/internal/analysis"
	"activity-descriptor/internal/auth"
)

type regressionSkill struct {
	Skill        string                           `json:"skill"`
	Samples      int                              `json:"samples"`
	R2           float64                          `json:"r2"`
	Intercept    float64                          `json:"intercept"`
	Coefficients []analysis.RegressionCoefficient `json:"coefficients"`
}

type regressionResponse struct {
	Target      string            `json:"target"`
	Description string            `json:"description"`
	Samples     int               `json:"samples"`
	Skills      []regressionSkill `json:"skills"`
}

// handleRegression fits an explainable OLS model for skill demand. Target is
// binary (skill present in a vacancy); predictors are experience, salary and
// Moscow region. It is a descriptive model, not a hiring prediction.
func (s *Server) handleRegression(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleSpecialist, auth.RoleUser) {
		return
	}
	pgStore, ok := s.store.(*PGStore)
	if !ok || pgStore.Pool == nil {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "регрессия требует PostgreSQL")
		return
	}
	rows, err := pgStore.Pool.Query(r.Context(), `
		SELECT name, description, area, experience, salary_from, salary_to, parsed_skills, key_skills
		FROM vacancy_catalog
		WHERE experience <> ''`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось загрузить вакансии")
		return
	}
	defer rows.Close()
	catalog := analysis.NewTaxonomyCatalog(nil, analysis.DefaultTaxonomy())
	type vacancy struct {
		skills []string
		salary float64
		middle bool
		senior bool
		moscow bool
	}
	vacancies := make([]vacancy, 0, 256)
	skillCounts := make(map[string]int)
	for rows.Next() {
		var name, description, area, experience string
		var parsedRaw, keyRaw []byte
		var salaryFrom, salaryTo *int64
		if err := rows.Scan(&name, &description, &area, &experience, &salaryFrom, &salaryTo, &parsedRaw, &keyRaw); err != nil {
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось прочитать вакансии")
			return
		}
		sk := make(map[string]bool)
		for _, item := range decodeStringArray(parsedRaw) {
			sk[strings.ToLower(item)] = true
		}
		for _, item := range decodeStringArray(keyRaw) {
			sk[strings.ToLower(item)] = true
		}
		for skill := range analysis.ExtractSkills([]string{name + " " + description}, catalog).Freq {
			sk[strings.ToLower(skill)] = true
		}
		skills := make([]string, 0, len(sk))
		for skill := range sk {
			skills = append(skills, skill)
			skillCounts[skill]++
		}
		salary := 0.0
		if salaryFrom != nil && *salaryFrom > 0 {
			salary = float64(*salaryFrom)
		}
		if salaryTo != nil && *salaryTo > 0 {
			if salary == 0 {
				salary = float64(*salaryTo)
			} else {
				salary = (salary + float64(*salaryTo)) / 2
			}
		}
		vacancies = append(vacancies, vacancy{
			skills: skills, salary: salary,
			middle: strings.EqualFold(experience, "middle"),
			senior: strings.EqualFold(experience, "senior"),
			moscow: strings.Contains(strings.ToLower(area), "москва"),
		})
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось прочитать вакансии")
		return
	}

	target := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("skill")))
	skills := make([]string, 0, len(skillCounts))
	for skill := range skillCounts {
		skills = append(skills, skill)
	}
	sort.Slice(skills, func(i, j int) bool {
		if skillCounts[skills[i]] == skillCounts[skills[j]] {
			return skills[i] < skills[j]
		}
		return skillCounts[skills[i]] > skillCounts[skills[j]]
	})
	if target != "" {
		skills = []string{target}
	} else if len(skills) > 20 {
		skills = skills[:20]
	}
	response := regressionResponse{
		Target:      "наличие навыка в вакансии",
		Description: "OLS: 1 = навык указан, 0 = не указан. Базовый уровень опыта - junior, базовая локация - не Москва.",
		Samples:     len(vacancies),
		Skills:      make([]regressionSkill, 0, len(skills)),
	}
	for _, skill := range skills {
		y := make([]float64, 0, len(vacancies))
		x := make([][]float64, 0, len(vacancies))
		for _, item := range vacancies {
			present := 0.0
			for _, current := range item.skills {
				if current == skill {
					present = 1
					break
				}
			}
			y = append(y, present)
			x = append(x, []float64{
				boolFeature(item.middle), boolFeature(item.senior),
				math.Log1p(item.salary / 1000), boolFeature(item.moscow),
			})
		}
		result := analysis.OLS(y, x, []string{"опыт: middle", "опыт: senior", "зарплата (лог)", "Москва"})
		if result.Samples == 0 {
			continue
		}
		if len(result.Coefficients) == 0 {
			continue
		}
		response.Skills = append(response.Skills, regressionSkill{
			Skill: skill, Samples: result.Samples, R2: result.R2,
			Intercept: result.Intercept, Coefficients: result.Coefficients,
		})
	}
	sort.Slice(response.Skills, func(i, j int) bool {
		return math.Abs(response.Skills[i].Coefficients[0].Estimate) > math.Abs(response.Skills[j].Coefficients[0].Estimate)
	})
	writeJSON(w, http.StatusOK, response)
}

func decodeStringArray(raw []byte) []string {
	var values []string
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil
	}
	return values
}

func boolFeature(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
