package domain

import (
	"errors"
	"testing"
)

func TestПравилаПлоскогоДерева(t *testing.T) {
	parent := "r"
	valid := func() []Node {
		return []Node{{ID: "r", Type: NodeRole, Title: "Роль", Evidence: []Evidence{{ID: "e1", SourceID: "s", Quote: "цитата"}}}, {ID: "c", Type: NodeFunction, Title: "Функция", ParentID: &parent, Evidence: []Evidence{{ID: "e2", SourceID: "s", Quote: "цитата"}}}}
	}
	cases := []struct {
		name, code string
		change     func([]Node) []Node
	}{
		{"пустое название", "EMPTY_TITLE", func(n []Node) []Node { n[1].Title = " "; return n }},
		{"нет подтверждения", "MISSING_EVIDENCE", func(n []Node) []Node { n[1].Evidence = nil; return n }},
		{"сирота", "ORPHAN_NODE", func(n []Node) []Node { p := "missing"; n[1].ParentID = &p; return n }},
		{"цикл", "CYCLE_DETECTED", func(n []Node) []Node { p := "c"; n[0].ParentID = &p; return n }},
		{"дубль названия", "DUPLICATE_SIBLING_TITLE", func(n []Node) []Node {
			v := n[1]
			v.ID = "c2"
			v.Evidence = []Evidence{{ID: "e3", SourceID: "s", Quote: "цитата"}}
			return append(n, v)
		}},
	}
	if err := ValidateTree(valid()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTree(tc.change(valid()))
			var v *TreeValidationError
			if !errors.As(err, &v) || v.Code != tc.code {
				t.Fatalf("%v", err)
			}
		})
	}
}
