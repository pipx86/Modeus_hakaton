package analysis

import (
	"strings"
	"testing"

	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestGroundedTreeReferencesOriginalSource(t *testing.T) {
	source := "00000000-0000-4000-8000-000000000001"
	description := "Мы ищем специалиста: разработка на Go и работа с PostgreSQL."
	nodes, err := BuildGroundedTree("Разработчик", []domain.VacancyRecord{{SourceID: source, Description: description}}, [][]string{{"go", "postgresql"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 6 {
		t.Fatalf("ожидалось 6 узлов, получено %d", len(nodes))
	}
	if err := domain.ValidateTree(nodes); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, n := range nodes {
		var id pgtype.UUID
		if id.Scan(n.ID) != nil || !id.Valid || ids[n.ID] {
			t.Fatalf("неверный ID узла: %s", n.ID)
		}
		ids[n.ID] = true
		for _, e := range n.Evidence {
			if id.Scan(e.ID) != nil || !id.Valid || ids[e.ID] {
				t.Fatalf("неверный ID подтверждения: %s", e.ID)
			}
			ids[e.ID] = true
			if e.SourceID != source || !strings.Contains(description, e.Quote) {
				t.Fatalf("неверное подтверждение: %+v", e)
			}
		}
	}
}

func TestGroundedTreeRejectsMissingSourceAndInventedSkill(t *testing.T) {
	_, err := BuildGroundedTree("Аналитик", []domain.VacancyRecord{{SourceID: "нет", Description: "Python"}}, [][]string{{"python"}})
	if err == nil {
		t.Fatal("несуществующий источник нельзя использовать")
	}
	_, err = BuildGroundedTree("Аналитик", []domain.VacancyRecord{{SourceID: "00000000-0000-4000-8000-000000000001", Description: "Python"}}, [][]string{{"go"}})
	if err == nil {
		t.Fatal("нельзя ссылаться на отсутствующий в тексте навык")
	}
}
