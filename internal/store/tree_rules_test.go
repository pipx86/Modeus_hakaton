package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestПравилаДерева(t *testing.T) {
	identifier := func(n int) pgtype.UUID {
		var id pgtype.UUID
		if err := id.Scan(fmt.Sprintf("00000000-0000-4000-8000-%012d", n)); err != nil {
			t.Fatal(err)
		}
		return id
	}
	node := func(n int, name string) TreeNode {
		return TreeNode{ID: identifier(n), Title: name, Evidence: []TreeEvidence{{ID: identifier(n + 100), SourceID: identifier(999), Quote: "цитата"}}}
	}
	cases := []struct {
		name  string
		build func() TreeNode
		code  string
	}{
		{"корректное дерево", func() TreeNode {
			r := node(1, "роль")
			r.Children = []TreeNode{node(2, "функция")}
			return r
		}, ""},
		{"пустое название", func() TreeNode { return node(1, "   ") }, "EMPTY_TITLE"},
		{"без подтверждения", func() TreeNode { r := node(1, "роль"); r.Evidence = nil; return r }, "MISSING_EVIDENCE"},
		{"одинаковые названия у родителя", func() TreeNode {
			r := node(1, "роль")
			r.Children = []TreeNode{node(2, "функция"), node(3, " функция ")}
			return r
		}, "DUPLICATE_SIBLING_TITLE"},
		{"повтор идентификатора", func() TreeNode {
			r := node(1, "роль")
			r.Children = []TreeNode{node(2, "одна"), node(2, "другая")}
			return r
		}, "DUPLICATE_NODE_ID"},
		{"цикл", func() TreeNode {
			r := node(1, "роль")
			r.Children = make([]TreeNode, 1)
			r.Children[0] = r
			return r
		}, "CYCLE_DETECTED"},
		{"лишний уровень", func() TreeNode {
			a := node(1, "a")
			b := node(2, "b")
			c := node(3, "c")
			d := node(4, "d")
			e := node(5, "e")
			d.Children = []TreeNode{e}
			c.Children = []TreeNode{d}
			b.Children = []TreeNode{c}
			a.Children = []TreeNode{b}
			return a
		}, "INVALID_TREE_DEPTH"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTree(tc.build())
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var validation *TreeValidationError
			if !errors.As(err, &validation) || validation.Code != tc.code {
				t.Fatalf("ожидался %s, получено %v", tc.code, err)
			}
		})
	}
}

func TestОшибкаПроверкиДереваСодержитКод(t *testing.T) {
	err := (&TreeValidationError{Code: "EMPTY_TITLE", Details: "название узла пустое"}).Error()
	if !strings.Contains(err, "EMPTY_TITLE") || !strings.Contains(err, "название узла пустое") {
		t.Fatalf("неверное описание ошибки: %s", err)
	}
}
