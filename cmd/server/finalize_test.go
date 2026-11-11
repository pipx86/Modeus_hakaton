package main

import (
	"testing"

	"activity-descriptor/internal/domain"
)

func TestTranscriptFromSession(t *testing.T) {
	session := domain.InterviewSession{
		Questions: []domain.Question{{Text: "Что вы делаете?"}, {Text: "Какие задачи?"}},
		Answers:   []domain.Answer{{Text: "Анализирую данные."}, {Text: "Строю отчёты."}},
	}
	got := transcriptFromSession(session)
	want := "Вопрос: Что вы делаете?\nОтвет: Анализирую данные.\nВопрос: Какие задачи?\nОтвет: Строю отчёты.\n"
	if got != want {
		t.Fatalf("transcript = %q, want %q", got, want)
	}
}

func TestAssignUUIDsPreservesHierarchyAndEvidence(t *testing.T) {
	parent := "role-1"
	nodes := []domain.Node{
		{ID: parent, Type: domain.NodeRole, Title: "Аналитик", Evidence: []domain.Evidence{{ID: "e1", SourceID: "src", Quote: "q1"}}},
		{ID: "task-1", Type: domain.NodeTask, Title: "Отчёты", ParentID: &parent, Evidence: []domain.Evidence{{ID: "e2", SourceID: "src", Quote: "q2"}}},
	}
	sourceID := newUUID()
	out := assignUUIDs(nodes, sourceID)
	if len(out) != 2 {
		t.Fatalf("len = %d", len(out))
	}
	if out[0].ID == parent || out[1].ID == "task-1" {
		t.Fatal("node IDs were not replaced with UUIDs")
	}
	if out[1].ParentID == nil || *out[1].ParentID != out[0].ID {
		t.Fatalf("parent was not remapped: %+v", out[1].ParentID)
	}
	for _, node := range out {
		for _, evidence := range node.Evidence {
			if evidence.SourceID != sourceID.String() {
				t.Fatalf("source id = %q, want %q", evidence.SourceID, sourceID.String())
			}
			if evidence.ID == "" || evidence.ID == "e1" || evidence.ID == "e2" {
				t.Fatalf("evidence ID was not replaced: %q", evidence.ID)
			}
		}
	}
}
