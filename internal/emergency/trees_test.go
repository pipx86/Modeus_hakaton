package emergency

import (
	"strings"
	"testing"

	"activity-descriptor/internal/domain"
)

func TestTreeSelectsProfessionAndLevel(t *testing.T) {
	cases := []struct {
		profession string
		role       string
	}{
		{"Backend-разработчик (junior)", "Backend-разработчик (Junior)"},
		{"Backend-разработчик", "Backend-разработчик (Middle)"},
		{"Backend-разработчик (senior)", "Backend-разработчик (Senior)"},
		{"Аналитик данных", "Аналитик данных"},
		{"DevOps-инженер", "DevOps-инженер"},
	}
	for _, test := range cases {
		t.Run(test.profession, func(t *testing.T) {
			nodes := Tree(test.profession, domain.InterviewSession{Answers: []domain.Answer{{Text: "Ответ"}}})
			if len(nodes) != 22 {
				t.Fatalf("nodes = %d, want 22", len(nodes))
			}
			if nodes[0].Title != test.role {
				t.Fatalf("role = %q, want %q", nodes[0].Title, test.role)
			}
			functions, tasks, actions := 0, 0, 0
			for _, node := range nodes {
				switch node.Type {
				case domain.NodeFunction:
					functions++
				case domain.NodeTask:
					tasks++
				case domain.NodeAction:
					actions++
				}
				if !strings.Contains(node.Evidence[0].Quote, "Ответ") {
					t.Fatalf("quote was not taken from the answer: %+v", node.Evidence[0])
				}
			}
			if functions != 3 || tasks != 6 || actions != 12 {
				t.Fatalf("functions=%d tasks=%d actions=%d", functions, tasks, actions)
			}
		})
	}
}
