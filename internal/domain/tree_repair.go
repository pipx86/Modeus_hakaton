package domain

import (
	"fmt"
	"strings"
)

// repairLevelTypes задаёт тип узла для каждого уровня дерева.
var repairLevelTypes = []NodeType{NodeRole, NodeFunction, NodeTask, NodeAction}

// RepairTree приводит дерево к контракту роль → функция → задача → действие.
//
// Модель иногда возвращает корректный JSON, но с нарушенной иерархией: корень
// типа task, несколько корней, сироты, слишком глубокая вложенность. Такая
// структура отклоняется валидацией при сохранении, поэтому перед записью дерево
// чинится детерминированно: выбирается один корень-роль, остальные корни и
// сироты подвешиваются к нему, лишняя глубина подрезается до уровня задачи,
// а тип каждого узла выставляется по его фактическому уровню.
func RepairTree(nodes []Node) ([]Node, error) {
	if len(nodes) == 0 {
		return nodes, nil
	}

	out := make([]Node, len(nodes))
	copy(out, nodes)
	out = dedupeSiblingTitles(out)

	byID := make(map[string]int, len(out))
	for index := range out {
		if out[index].ID == "" {
			return nil, fmt.Errorf("repair: узел без id (title=%q)", out[index].Title)
		}
		if _, duplicate := byID[out[index].ID]; duplicate {
			return nil, fmt.Errorf("repair: дублирующийся id %q", out[index].ID)
		}
		byID[out[index].ID] = index
	}

	root := -1
	for index := range out {
		if out[index].ParentID == nil || *out[index].ParentID == "" {
			root = index
			break
		}
	}
	if root == -1 {
		root = 0
	}
	rootID := out[root].ID
	out[root].ParentID = nil
	out[root].Type = NodeRole

	for index := range out {
		if index == root {
			continue
		}
		parent := out[index].ParentID
		if parent == nil || *parent == "" {
			out[index].ParentID = &rootID
			continue
		}
		if _, ok := byID[*parent]; !ok {
			out[index].ParentID = &rootID
		}
	}

	depth := make([]int, len(out))
	state := make([]int, len(out))

	var calcDepth func(index int) int
	calcDepth = func(index int) int {
		switch state[index] {
		case 2:
			return depth[index]
		case 1:
			// Цикл: подвешиваем узел к корню и выходим.
			out[index].ParentID = &rootID
			depth[index] = 1
			state[index] = 2
			return 1
		}
		state[index] = 1

		level := 0
		if parent := out[index].ParentID; parent != nil && *parent != "" {
			if parentIndex, ok := byID[*parent]; ok {
				level = calcDepth(parentIndex) + 1
			}
		}

		if level > 3 {
			// Поднимаемся к предку уровня задачи (глубина 2).
			current := index
			for {
				parent := out[current].ParentID
				if parent == nil {
					break
				}
				parentIndex, ok := byID[*parent]
				if !ok {
					break
				}
				if depth[parentIndex] <= 2 {
					current = parentIndex
					break
				}
				current = parentIndex
			}
			parentID := out[current].ID
			out[index].ParentID = &parentID
			level = 3
		}

		depth[index] = level
		state[index] = 2
		return level
	}

	for index := range out {
		calcDepth(index)
	}

	for index := range out {
		level := depth[index]
		if level < 0 {
			level = 0
		}
		if level >= len(repairLevelTypes) {
			level = len(repairLevelTypes) - 1
		}
		out[index].Type = repairLevelTypes[level]
	}

	return out, nil
}

// dedupeSiblingTitles merges nodes that have the same parent and title. A
// vacancy-focused analyzer pass often discovers the same task that the
// interview pass already found; keeping both makes the tree invalid
// (DUPLICATE_SIBLING_TITLE), while dropping the second copy loses its evidence.
func dedupeSiblingTitles(nodes []Node) []Node {
	seen := make(map[string]int, len(nodes))
	redirect := make(map[string]string, len(nodes))
	out := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		parentKey := ""
		if node.ParentID != nil {
			parentKey = *node.ParentID
		}
		key := parentKey + "\x00" + strings.ToLower(strings.TrimSpace(node.Title))
		if parentKey != "" {
			if index, ok := seen[key]; ok {
				out[index].Evidence = appendUniqueEvidence(out[index].Evidence, node.Evidence)
				redirect[node.ID] = out[index].ID
				continue
			}
		}
		seen[key] = len(out)
		out = append(out, node)
	}
	if len(redirect) == 0 {
		return out
	}
	for index := range out {
		if out[index].ParentID == nil {
			continue
		}
		parent := *out[index].ParentID
		for {
			next, ok := redirect[parent]
			if !ok {
				break
			}
			parent = next
		}
		out[index].ParentID = &parent
	}
	return out
}

func appendUniqueEvidence(existing, incoming []Evidence) []Evidence {
	known := make(map[string]bool, len(existing))
	for _, item := range existing {
		known[item.ID] = true
	}
	for _, item := range incoming {
		if known[item.ID] {
			continue
		}
		known[item.ID] = true
		existing = append(existing, item)
	}
	return existing
}
