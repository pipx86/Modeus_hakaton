package domain

import "testing"

func ptr(value string) *string { return &value }

func TestRepairTreeFixesRootType(t *testing.T) {
	nodes := []Node{
		{ID: "root", Type: NodeTask, Title: "Task as root"},
		{ID: "child", Type: NodeFunction, Title: "Child", ParentID: ptr("root")},
	}

	repaired, err := RepairTree(nodes)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if repaired[0].Type != NodeRole {
		t.Fatalf("корень должен стать role, получили %q", repaired[0].Type)
	}
	if repaired[1].Type != NodeFunction {
		t.Fatalf("ребёнок должен остаться function, получили %q", repaired[1].Type)
	}
}

func TestRepairTreeMergesExtraRoots(t *testing.T) {
	nodes := []Node{
		{ID: "role", Type: NodeRole, Title: "Role"},
		{ID: "second", Type: NodeTask, Title: "Second root"},
	}

	repaired, err := RepairTree(nodes)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	roots := 0
	for _, node := range repaired {
		if node.ParentID == nil {
			roots++
		}
	}
	if roots != 1 {
		t.Fatalf("ожидали один корень, получили %d", roots)
	}
	if repaired[1].ParentID == nil || *repaired[1].ParentID != "role" {
		t.Fatalf("второй корень должен подвеситься к role")
	}
	if repaired[1].Type != NodeFunction {
		t.Fatalf("узел уровня 1 должен стать function, получили %q", repaired[1].Type)
	}
}

func TestRepairTreeAttachesOrphans(t *testing.T) {
	nodes := []Node{
		{ID: "role", Type: NodeRole, Title: "Role"},
		{ID: "orphan", Type: NodeAction, Title: "Orphan", ParentID: ptr("missing")},
	}

	repaired, err := RepairTree(nodes)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if repaired[1].ParentID == nil || *repaired[1].ParentID != "role" {
		t.Fatalf("сирота должна подвеситься к корню")
	}
}

func TestRepairTreeClampsTooDeepNodes(t *testing.T) {
	nodes := []Node{
		{ID: "role", Type: NodeRole, Title: "Role"},
		{ID: "fn", Type: NodeFunction, Title: "Fn", ParentID: ptr("role")},
		{ID: "task", Type: NodeTask, Title: "Task", ParentID: ptr("fn")},
		{ID: "action", Type: NodeAction, Title: "Action", ParentID: ptr("task")},
		{ID: "too-deep", Type: NodeAction, Title: "Too deep", ParentID: ptr("action")},
	}

	repaired, err := RepairTree(nodes)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	deep := repaired[4]
	if deep.ParentID == nil || *deep.ParentID != "task" {
		t.Fatalf("слишком глубокий узел должен подвеситься к задаче, родитель: %v", deep.ParentID)
	}
	if deep.Type != NodeAction {
		t.Fatalf("ожидали action на уровне 3, получили %q", deep.Type)
	}
}
