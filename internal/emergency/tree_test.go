package emergency

import (
	"testing"

	"activity-descriptor/internal/domain"
)

func TestTreeUsesRealInterviewAnswers(t *testing.T) {
	session := domain.InterviewSession{
		Profession: "Аналитик данных",
		Questions:  []domain.Question{{ID: "q-1", Text: "Чем вы занимаетесь?"}},
		Answers: []domain.Answer{
			{QuestionID: "q-1", Text: "Я аналитик данных."},
			{QuestionID: "q-1", Text: "Отвечаю за отчётность."},
			{QuestionID: "q-1", Text: "Строю SQL-запросы."},
			{QuestionID: "q-1", Text: "Проверяю качество данных."},
		},
	}
	nodes := Tree(session.Profession, session)
	if len(nodes) != 22 {
		t.Fatalf("nodes = %d", len(nodes))
	}
	if nodes[0].Type != domain.NodeRole || nodes[3].Type != domain.NodeAction {
		t.Fatalf("types = %+v", nodes)
	}
	for _, node := range nodes {
		if len(node.Evidence) != 1 || node.Evidence[0].Quote == "" {
			t.Fatalf("node without evidence: %+v", node)
		}
		if node.Evidence[0].SourceID != SourceID {
			t.Fatalf("source = %q", node.Evidence[0].SourceID)
		}
	}
	if nodes[1].ParentID == nil || *nodes[1].ParentID != nodes[0].ID {
		t.Fatal("function is not attached to role")
	}
}
