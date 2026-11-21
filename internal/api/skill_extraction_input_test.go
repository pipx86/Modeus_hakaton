package api

import (
	"activity-descriptor/internal/domain"
	"encoding/json"
	"strings"
	"testing"
)

func TestSkillExtractionUsesExactOpaqueReferences(t *testing.T) {
	nodes := []domain.Node{{ID: "original-node-uuid", Type: domain.NodeTask, Title: "Task", Zun: &domain.Zun{Knowledge: []string{"legacy unsupported claim"}}, Evidence: []domain.Evidence{{ID: "original-evidence-uuid", SourceID: "original-source-uuid", Quote: "source quote", InterviewID: "private-interview-id"}, {ID: "rejected-id", SourceID: "source", Quote: "rejected quote", Status: domain.EvidenceRejected}}}}
	input, nodeIDs, evidenceIDs := skillExtractionInput(nodes)
	if len(input) != 1 || len(input[0].Evidence) != 1 || nodeIDs["node_1"] != nodes[0].ID || evidenceIDs["evidence_1"] != nodes[0].Evidence[0].ID {
		t.Fatal(input, nodeIDs, evidenceIDs)
	}
	raw, _ := json.Marshal(input)
	for _, forbidden := range []string{"original-", "private-interview", "legacy unsupported", "rejected quote"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("unnecessary metadata reached input: %s", forbidden)
		}
	}
	if nodes[0].Evidence[0].ID != "original-evidence-uuid" || len(nodes[0].Evidence) != 2 {
		t.Fatal("mutated original tree")
	}
}
