package analysis

import (
	"testing"

	"activity-descriptor/internal/domain"
)

func TestBuildActivityTreeShape(t *testing.T) {
	levels := []map[string]any{
		{"experience": "junior", "skills": []string{"python", "sql"}},
		{"experience": "senior", "skills": []string{"architecture"}},
	}
	reports := []GapReport{
		{
			ProfileName: "Ivan",
			Recommendations: []Recommendation{
				{Skill: "docker", Priority: 0.8, Reason: "missing in profile"},
			},
		},
	}

	nodes, err := BuildActivityTree("backend developer", levels, reports)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 1 root (role) + 2 functions + 3 tasks (python/sql/architecture) + 1 action (docker) = 7
	if len(nodes) != 7 {
		t.Fatalf("ожидали 7 узлов, получили %d", len(nodes))
	}

	// Проверяем корень: ParentID должен быть nil.
	if nodes[0].Type != domain.NodeRole || nodes[0].ParentID != nil {
		t.Fatalf("первый узел обязан быть корнем role без родителя: %+v", nodes[0])
	}

	// Считаем типы.
	counts := map[domain.NodeType]int{}
	for _, n := range nodes {
		counts[n.Type]++
	}
	if counts[domain.NodeRole] != 1 || counts[domain.NodeFunction] != 2 ||
		counts[domain.NodeTask] != 3 || counts[domain.NodeAction] != 1 {
		t.Fatalf("распределение типов сломано: %+v", counts)
	}

	// Все children обязаны ссылаться на существующего parent через *string.
	idSet := map[string]bool{}
	for _, n := range nodes {
		idSet[n.ID] = true
	}
	for _, n := range nodes {
		if n.ParentID != nil && !idSet[*n.ParentID] {
			t.Fatalf("узел %s ссылается на несуществующего parent %s", n.ID, *n.ParentID)
		}
	}
}

func TestSlugifyDeterministic(t *testing.T) {
	a := slugify("Backend Developer!")
	b := slugify("backend-developer")
	if a != b {
		t.Fatalf("slugify должен быть детерминированным: %q vs %q", a, b)
	}
}

func TestTitleCase(t *testing.T) {
	got := titleCase("backend developer")
	if got != "Backend Developer" {
		t.Fatalf("titleCase ожидали \"Backend Developer\", получили %q", got)
	}
}
