package synthesizer

import (
	"testing"

	"activity-descriptor/internal/domain"
)

// Карточка 9: спор «задача или действие» по одному и тому же id должен попадать
// в конфликты, а не разрешаться молча в пользу одного из источников.

func TestDetectConflictsReportsTypeDisagreement(t *testing.T) {
	groups := map[string][]sourcedNode{
		"task-1": {
			{
				source: domain.Source{ID: "std-1", Type: domain.SourceStandard},
				node:   domain.Node{ID: "task-1", Type: domain.NodeTask, Title: "Собирать требования"},
			},
			{
				source: domain.Source{ID: "vac-1", Type: domain.SourceVacancy},
				node:   domain.Node{ID: "task-1", Type: domain.NodeAction, Title: "Собирать требования"},
			},
		},
	}

	conflicts := DetectConflicts(groups, []string{"task-1"})
	var typeConflict *domain.Conflict
	for i := range conflicts {
		if conflicts[i].Field == "type" {
			typeConflict = &conflicts[i]
		}
	}
	if typeConflict == nil {
		t.Fatalf("ожидался конфликт типа, получено: %+v", conflicts)
	}
	if len(typeConflict.Values) != 2 {
		t.Fatalf("обе позиции должны сохраниться: %+v", typeConflict.Values)
	}
	if typeConflict.Winner.Value != string(domain.NodeTask) {
		t.Fatalf("побеждает значение источника с более высоким приоритетом: %+v", typeConflict.Winner)
	}
	values := map[string]bool{}
	for _, value := range typeConflict.Values {
		values[value.Value] = true
	}
	if !values[string(domain.NodeTask)] || !values[string(domain.NodeAction)] {
		t.Fatalf("в конфликте должны быть оба типа: %+v", values)
	}
}

func TestDetectConflictsIgnoresSameType(t *testing.T) {
	groups := map[string][]sourcedNode{
		"task-1": {
			{
				source: domain.Source{ID: "std-1", Type: domain.SourceStandard},
				node:   domain.Node{ID: "task-1", Type: domain.NodeTask, Title: "Собирать требования"},
			},
			{
				source: domain.Source{ID: "vac-1", Type: domain.SourceVacancy},
				node:   domain.Node{ID: "task-1", Type: domain.NodeTask, Title: "Собирать требования"},
			},
		},
	}

	for _, conflict := range DetectConflicts(groups, []string{"task-1"}) {
		if conflict.Field == "type" {
			t.Fatalf("при совпадении типов конфликта типа быть не должно: %+v", conflict)
		}
	}
}
