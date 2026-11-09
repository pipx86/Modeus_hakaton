package store

import (
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// TreeValidationError содержит устойчивый код и сведения о проблемном узле.
type TreeValidationError struct {
	Code    string
	NodeID  pgtype.UUID
	Details string
}

func (e *TreeValidationError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Details) }

// ValidateTree проверяет дерево перед сохранением. Родитель задаётся вложенностью.
func ValidateTree(root TreeNode) error {
	seen := map[pgtype.UUID]bool{}
	active := map[pgtype.UUID]bool{}
	evidenceIDs := map[pgtype.UUID]bool{}
	var visit func(TreeNode, int) error
	visit = func(n TreeNode, depth int) error {
		fail := func(code, details string) error {
			return &TreeValidationError{Code: code, NodeID: n.ID, Details: details}
		}
		if !n.ID.Valid {
			return fail("INVALID_NODE_ID", "у узла нет идентификатора")
		}
		if active[n.ID] {
			return fail("CYCLE_DETECTED", "узел повторяется среди своих предков")
		}
		if seen[n.ID] {
			return fail("DUPLICATE_NODE_ID", "идентификатор узла повторяется")
		}
		if depth > 3 {
			return fail("INVALID_TREE_DEPTH", "дерево глубже уровня действия")
		}
		if strings.TrimSpace(n.Title) == "" {
			return fail("EMPTY_TITLE", "название узла пустое")
		}
		if len(n.Evidence) == 0 {
			return fail("MISSING_EVIDENCE", "у узла нет подтверждения")
		}
		for _, e := range n.Evidence {
			if !e.ID.Valid || !e.SourceID.Valid {
				return fail("INVALID_EVIDENCE", "у подтверждения или источника нет идентификатора")
			}
			if evidenceIDs[e.ID] {
				return fail("DUPLICATE_EVIDENCE_ID", "идентификатор подтверждения повторяется")
			}
			evidenceIDs[e.ID] = true
		}
		seen[n.ID], active[n.ID] = true, true
		defer delete(active, n.ID)
		titles := map[string]bool{}
		for _, child := range n.Children {
			title := strings.TrimSpace(child.Title)
			if title != "" && titles[title] {
				return &TreeValidationError{Code: "DUPLICATE_SIBLING_TITLE", NodeID: child.ID, Details: "у одного родителя есть узлы с одинаковым названием"}
			}
			titles[title] = true
		}
		for _, child := range n.Children {
			if err := visit(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(root, 0)
}
