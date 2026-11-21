package domain

import (
	"encoding/json"
	"testing"
)

func skillFixture() SkillDefinition {
	return SkillDefinition{ID: "transaction", Version: 1, Title: "Транзакции", Kind: "skill", Action: "Реализовать", Object: "транзакцию", Conditions: "ошибка между операциями", Result: "нет частичных изменений", Criterion: "проверка отката", Tools: []SkillToolUse{{ToolID: "postgresql", Usage: "хранение данных", Required: true}}}
}
func TestSkillComparison(t *testing.T) {
	a := skillFixture()
	cases := []struct {
		name   string
		change func(*SkillDefinition)
		want   string
	}{
		{"same", func(*SkillDefinition) {}, "full"},
		{"different role action", func(d *SkillDefinition) { d.Action = "Спроектировать" }, "not_established"},
		{"knowledge not proficiency", func(d *SkillDefinition) { d.Kind = "knowledge" }, "partial"},
		{"missing conditions", func(d *SkillDefinition) { d.Conditions = "" }, "not_established"},
		{"technology missing", func(d *SkillDefinition) { d.Tools = nil }, "partial"},
		{"illustrative is not guaranteed", func(d *SkillDefinition) {
			d.Tools = []SkillToolUse{{ToolID: "postgresql", Usage: "хранение данных", Required: false}}
		}, "partial"},
		{"same tool different use", func(d *SkillDefinition) {
			d.Tools = []SkillToolUse{{ToolID: "postgresql", Usage: "репликация", Required: true}}
		}, "partial"},
		{"normalization", func(d *SkillDefinition) { d.Action = "  РЕАЛИЗОВАТЬ " }, "full"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := a
			c.change(&b)
			got := CompareSkills(a, b)
			if got.Status != c.want || len(got.Reasons) == 0 {
				t.Fatalf("%+v", got)
			}
		})
	}
	a.Tools[0].Required = false
	b := a
	b.Tools = nil
	if CompareSkills(a, b).Status != "full" {
		t.Fatal("illustrative tool must not be required")
	}
}
func TestSkillModelValidation(t *testing.T) {
	previous := EmptySkillModel()
	model := EmptySkillModel()
	model.Definitions = []SkillDefinition{skillFixture()}
	model.Links = []SkillNodeLink{{ID: "link", NodeID: "n", SkillRef: SkillRef{ID: "transaction", Version: 1}, EvidenceIDs: []string{"e"}, Basis: "requirement", Reason: "описано сохранение", Status: "proposed"}}
	nodes := []Node{{ID: "n", Evidence: []Evidence{{ID: "e", Quote: "транзакция", Status: EvidencePending}}}}
	if err := ValidateSkillModel(model, previous, nodes); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(model.Links[0])
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	if payload["id"] != "link" || payload["definition_id"] != "transaction" {
		t.Fatal(string(raw))
	}
	previous = model
	model.Definitions = append([]SkillDefinition{}, previous.Definitions...)
	model.Definitions[0].Title = "переопределение"
	if ValidateSkillModel(model, previous, nodes) == nil {
		t.Fatal("immutable version changed")
	}
	model = previous
	model.Links = append([]SkillNodeLink{}, model.Links...)
	model.Links[0].EvidenceIDs = []string{"foreign"}
	if ValidateSkillModel(model, EmptySkillModel(), nodes) == nil {
		t.Fatal("foreign evidence accepted")
	}
}
func TestSkillAliasesAndProficiency(t *testing.T) {
	model := EmptySkillModel()
	model.Tools = append(model.Tools, SkillTool{ID: "other", Name: "Other", Kind: "other", Aliases: []string{"POSTGRES"}})
	if ValidateSkillModel(model, EmptySkillModel(), nil) == nil {
		t.Fatal("ambiguous alias")
	}
	d := skillFixture()
	d.Kind = "proficiency"
	if len(d.Gaps()) != 2 {
		t.Fatal(d.Gaps())
	}
}
