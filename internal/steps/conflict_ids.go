package steps

import "activity-descriptor/internal/domain"

// RemapConflictNodeIDs заменяет synth-time идентификаторы узлов в конфликтах на
// реальные UUID сохранённого дерева. Синтезатор работает с id вида "role-1",
// а интерфейс методиста открывает узел по UUID из БД, поэтому без этого шага
// конфликт нельзя связать с деревом.
//
// Исходный срез не изменяется: код вызывают и chat-, и batch-путь, и оба могут
// хранить исходные конфликты в run.Data. Неизвестные id остаются как есть —
// лучше показать конфликт с synth-id, чем потерять разногласие.
func RemapConflictNodeIDs(conflicts []domain.Conflict, ids map[string]string) []domain.Conflict {
	if len(conflicts) == 0 || len(ids) == 0 {
		return conflicts
	}
	out := make([]domain.Conflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		if mapped, ok := ids[conflict.NodeID]; ok && mapped != "" {
			conflict.NodeID = mapped
		}
		out = append(out, conflict)
	}
	return out
}
