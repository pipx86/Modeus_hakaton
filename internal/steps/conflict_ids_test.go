package steps

import (
	"testing"

	"activity-descriptor/internal/domain"
)

func TestRemapConflictNodeIDs(t *testing.T) {
	conflicts := []domain.Conflict{
		{ID: "c-1", NodeID: "role-1", Field: "title"},
		{ID: "c-2", NodeID: "task-2", Field: "type"},
		{ID: "c-3", NodeID: "unknown", Field: "description"},
	}
	ids := map[string]string{"role-1": "11111111-1111-4111-8111-111111111111", "task-2": "22222222-2222-4222-8222-222222222222"}

	out := RemapConflictNodeIDs(conflicts, ids)
	if out[0].NodeID != ids["role-1"] || out[1].NodeID != ids["task-2"] {
		t.Fatalf("известные id должны замениться: %+v", out)
	}
	if out[2].NodeID != "unknown" {
		t.Fatalf("неизвестный id должен сохраниться: %q", out[2].NodeID)
	}
	if conflicts[0].NodeID != "role-1" {
		t.Fatalf("исходный срез не должен меняться: %q", conflicts[0].NodeID)
	}
	if out[0].Field != "title" || out[1].Field != "type" {
		t.Fatalf("остальные поля должны сохраниться: %+v", out)
	}
}

func TestRemapConflictNodeIDsWithoutMapping(t *testing.T) {
	conflicts := []domain.Conflict{{ID: "c-1", NodeID: "role-1"}}
	if out := RemapConflictNodeIDs(conflicts, nil); len(out) != 1 || out[0].NodeID != "role-1" {
		t.Fatalf("без карты конфликты должны вернуться как есть: %+v", out)
	}
	if out := RemapConflictNodeIDs(nil, map[string]string{"role-1": "uuid"}); out != nil {
		t.Fatalf("пустой список должен вернуться без изменений: %+v", out)
	}
	if out := RemapConflictNodeIDs(conflicts, map[string]string{"role-1": ""}); out[0].NodeID != "role-1" {
		t.Fatalf("пустое значение карты не должно затирать id: %+v", out)
	}
}
