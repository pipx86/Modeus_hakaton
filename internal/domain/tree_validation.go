package domain

import "strings"

// TreeValidationError сообщает устойчивый код и положение ошибки в дереве.
type TreeValidationError struct {
	Code    string `json:"code"`
	NodeID  string `json:"node_id,omitempty"`
	Details string `json:"details"`
}

func (e *TreeValidationError) Error() string { return e.Code + ": " + e.Details }

// ValidateTree проверяет плоский список до начала записи в хранилище.
func ValidateTree(nodes []Node) error {
	fail := func(code, id, details string) error { return &TreeValidationError{code, id, details} }
	if len(nodes) == 0 {
		return fail("ORPHAN_NODE", "", "у дерева нет корня")
	}
	byID := map[string]Node{}
	evidenceIDs := map[string]bool{}
	siblings := map[string]map[string]bool{}
	for _, n := range nodes {
		if strings.TrimSpace(n.ID) == "" {
			return fail("INVALID_NODE_ID", n.ID, "не задан идентификатор")
		}
		if _, ok := byID[n.ID]; ok {
			return fail("DUPLICATE_NODE_ID", n.ID, "идентификатор повторяется")
		}
		byID[n.ID] = n
		if strings.TrimSpace(n.Title) == "" {
			return fail("EMPTY_TITLE", n.ID, "название пустое")
		}
		if len(n.Evidence) == 0 {
			return fail("MISSING_EVIDENCE", n.ID, "нет подтверждения")
		}
		for _, e := range n.Evidence {
			if e.ID == "" || e.SourceID == "" || strings.TrimSpace(e.Quote) == "" || (e.RefID != "" && strings.TrimSpace(e.RefID) == "") {
				return fail("INVALID_EVIDENCE", n.ID, "нужны ID, источник и цитата")
			}
			if evidenceIDs[e.ID] {
				return fail("DUPLICATE_EVIDENCE_ID", n.ID, "подтверждение повторяется")
			}
			evidenceIDs[e.ID] = true
		}
		parent := ""
		if n.ParentID != nil {
			parent = *n.ParentID
		}
		if siblings[parent] == nil {
			siblings[parent] = map[string]bool{}
		}
		title := strings.TrimSpace(n.Title)
		if siblings[parent][title] {
			return fail("DUPLICATE_SIBLING_TITLE", n.ID, "у одного родителя повторяется название")
		}
		siblings[parent][title] = true
	}
	state := map[string]int{}
	depths := map[string]int{}
	var depth func(string) (int, error)
	depth = func(id string) (int, error) {
		if state[id] == 1 {
			return 0, fail("CYCLE_DETECTED", id, "обнаружен цикл")
		}
		if state[id] == 2 {
			return depths[id], nil
		}
		state[id] = 1
		n := byID[id]
		d := 0
		if n.ParentID != nil {
			if _, ok := byID[*n.ParentID]; !ok {
				return 0, fail("ORPHAN_NODE", id, "родитель отсутствует")
			}
			p, err := depth(*n.ParentID)
			if err != nil {
				return 0, err
			}
			d = p + 1
		}
		types := []NodeType{NodeRole, NodeFunction, NodeTask, NodeAction}
		if d > 3 || n.Type != types[d] {
			return 0, fail("INVALID_TREE_DEPTH", id, "тип не соответствует уровню")
		}
		depths[id] = d
		state[id] = 2
		return d, nil
	}
	roots := 0
	for _, n := range nodes {
		if _, err := depth(n.ID); err != nil {
			return err
		}
		if n.ParentID == nil {
			roots++
		}
	}
	if roots != 1 {
		return fail("ORPHAN_NODE", "", "нужен ровно один корень")
	}
	return nil
}
