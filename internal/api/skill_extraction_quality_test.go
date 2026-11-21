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

func TestNormalizeSkillExtractionToolReferences(t *testing.T) {
	candidate := domain.EmptySkillModel()
	candidate.Tools = append(candidate.Tools, domain.SkillTool{ID: "gitlab_ci", Name: "GitLab CI", Kind: "platform", Aliases: []string{}})
	saved := domain.SkillDefinition{ID: "saved", Version: 1, Kind: "skill", Tools: []domain.SkillToolUse{{ToolID: "unknown", Usage: "leave saved version untouched"}}}
	candidate.Definitions = []domain.SkillDefinition{
		{ID: "new", Version: 1, Kind: "skill", Tools: []domain.SkillToolUse{
			{ToolID: "Python", Usage: "cleaning", Required: true},
			{ToolID: "python", Usage: "validation", Required: false},
			{ToolID: "Postgres", Usage: "query data", Required: true},
			{ToolID: "GitLab CI", Usage: "run pipeline", Required: true},
			{ToolID: "unknown-tool", Usage: "unrecognized tool", Required: true},
		}},
		saved,
	}
	previous := domain.SkillModel{Definitions: []domain.SkillDefinition{saved}}

	normalizeSkillExtractionToolReferences(&candidate, previous)

	got := candidate.Definitions[0].Tools
	if len(got) != 4 {
		t.Fatalf("normalized tools = %+v, want Python merged plus PostgreSQL, GitLab CI and unknown", got)
	}
	if got[0].ToolID != "python" || got[0].Usage != "cleaning; validation" || !got[0].Required {
		t.Fatalf("Python usages = %+v", got[0])
	}
	if got[1].ToolID != "postgresql" || got[2].ToolID != "gitlab_ci" || got[3].ToolID != "unknown-tool" {
		t.Fatalf("tool refs = %+v", got)
	}
	if !reflect.DeepEqual(candidate.Definitions[1], saved) {
		t.Fatalf("saved definition changed: %+v", candidate.Definitions[1])
	}
}

func TestNormalizeSkillExtractionEvidenceOwners(t *testing.T) {
	nodes := []domain.Node{
		{ID: "node-a", Evidence: []domain.Evidence{{ID: "evidence-a"}}},
		{ID: "node-b", Evidence: []domain.Evidence{{ID: "evidence-b"}}},
	}
	links := []domain.SkillNodeLink{
		{ID: "wrong-node", NodeID: "node-a", EvidenceIDs: []string{"evidence-b"}},
		{ID: "cross-node", NodeID: "node-a", EvidenceIDs: []string{"evidence-a", "evidence-b"}},
	}

	got := normalizeSkillExtractionEvidenceOwners(links, nodes)

	if len(got) != 3 {
		t.Fatalf("grounded links = %+v, want moved link plus two split links", got)
	}
	if got[0].NodeID != "node-b" || !reflect.DeepEqual(got[0].EvidenceIDs, []string{"evidence-b"}) {
		t.Fatalf("wrong-node citation not moved to its owner: %+v", got[0])
	}
	if got[1].NodeID != "node-a" || !reflect.DeepEqual(got[1].EvidenceIDs, []string{"evidence-a"}) {
		t.Fatalf("first split link incorrect: %+v", got[1])
	}
	if got[2].NodeID != "node-b" || !reflect.DeepEqual(got[2].EvidenceIDs, []string{"evidence-b"}) {
		t.Fatalf("second split link incorrect: %+v", got[2])
	}
	for _, link := range got {
		if link.ID != "" {
			t.Fatalf("proposal ID assigned before splitting: %+v", link)
		}
	}
}
