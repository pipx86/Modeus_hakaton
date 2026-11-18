package domain

import "fmt"

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
