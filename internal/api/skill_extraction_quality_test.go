package api

import (
	"activity-descriptor/internal/domain"
	"reflect"
	"testing"
)

func TestDemoteIncompleteSkillProficiency(t *testing.T) {
	incomplete := domain.SkillDefinition{ID: "incomplete", Version: 1, Kind: "proficiency", Action: "восстанавливать", Object: "PostgreSQL", Conditions: "тестовый стенд", Result: "база восстановлена", Tools: []domain.SkillToolUse{}}
	complete := domain.SkillDefinition{ID: "complete", Version: 1, Kind: "proficiency", Action: "восстанавливать", Object: "PostgreSQL", Conditions: "полная и инкрементальная копии; повреждение файла", Result: "данные восстановлены", Criterion: "сверить контрольные суммы", Autonomy: "самостоятельно", Scope: "еженедельно в течение восьми недель в разных сценариях", Tools: []domain.SkillToolUse{}}
	saved := domain.SkillDefinition{ID: "saved", Version: 1, Kind: "proficiency", Tools: []domain.SkillToolUse{}}
	candidate := domain.SkillModel{Definitions: []domain.SkillDefinition{incomplete, complete, saved}}
	previous := domain.SkillModel{Definitions: []domain.SkillDefinition{saved}}

	demoteIncompleteSkillProficiency(&candidate, previous)

	if candidate.Definitions[0].Kind != "skill" {
		t.Fatalf("incomplete proficiency kind = %q, want skill", candidate.Definitions[0].Kind)
	}
	if candidate.Definitions[1].Kind != "proficiency" {
		t.Fatalf("complete proficiency demoted: %+v", candidate.Definitions[1])
	}
	if !reflect.DeepEqual(candidate.Definitions[2], saved) {
		t.Fatalf("previously saved definition changed: %+v", candidate.Definitions[2])
	}
}
