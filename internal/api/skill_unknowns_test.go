package api

import (
	"activity-descriptor/internal/domain"
	"strings"
	"testing"
)

func TestSkillUnknownStatementsDoNotSatisfyConditions(t *testing.T) {
	d := domain.SkillDefinition{ID: "sql", Version: 1, Kind: "skill", Conditions: "Конкретные условия задачи не обсуждались", Criterion: "Критерий не указан"}
	model := domain.EmptySkillModel()
	model.Definitions = []domain.SkillDefinition{d}
	model.Links = []domain.SkillNodeLink{{SkillRef: domain.SkillRef{ID: "sql", Version: 1}, Reason: "Пишет запросы"}}
	normalizeSkillUnknowns(&model, domain.EmptySkillModel())
	if model.Definitions[0].Conditions != "" || model.Definitions[0].Criterion != "" || !strings.Contains(model.Links[0].Reason, "уточнения") {
		t.Fatal(model)
	}
	model.Definitions[0] = d
	normalizeSkillUnknowns(&model, domain.SkillModel{Definitions: []domain.SkillDefinition{d}})
	if model.Definitions[0].Conditions != d.Conditions {
		t.Fatal("changed accepted definition")
	}
	model.Links[0].Reason = ""
	normalizeSkillUnknowns(&model, domain.EmptySkillModel())
	if model.Links[0].Reason != "" {
		t.Fatal("manufactured reason")
	}
}
