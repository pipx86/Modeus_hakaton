package api

import (
	"sort"
	"strings"

	"activity-descriptor/internal/domain"
)

// Семантическое сопоставление узлов двух независимых прогонов.
//
// Узлы дерева в PostgreSQL имеют глобальный первичный ключ nodes.id, поэтому
// два независимых прогона одной профессии никогда не делят идентификаторы
// узлов. Сравнение только по id показало бы «всё добавлено / всё удалено» и
// никогда — changed. Поэтому run-to-run comparison работает в два прохода:
//
//  1. матчинг по node.id (как snapshot diff) — для прогонов одной линии,
//     где узлы переиспользуют идентификаторы;
//  2. для оставшихся unmatched-узлов — семантический матчинг по ключу
//     type + нормализованное title + нормализованный путь предков
//     (ancestor-title path). Матчатся только однозначные пары: если на одной
//     из сторон под ключом больше одного узла, пара не считается найденной
//     (иначе был бы ложный changed). Такие узлы остаются added/removed.
//
// Для semantic-пары changed считается по полям title, description, type,
// parent_id, evidence:
//   - title/description/type сравниваются как есть, поэтому различие только в
//     регистре/пунктуации (совпал нормализованный ключ) даёт changed = title;
//   - parent_id для независимых прогонов всегда разный (UUID родителей
//     случайны), поэтому сравнивается семантически — нормализованный путь
//     предков; он входит в ключ матчинга, так что у найденной пары всегда
//     совпадает. Перемещение узла меняет путь предков, и узел не матчится, а
//     попадает в added/removed — так parent_id не даёт ложных changed;
//   - evidence сравнивается без технических идентификаторов (evidence.id,
//     node_id, source_id принадлежат прогону/источнику и всегда разные у
//     независимых прогонов): по паре (ref_id, quote, status), порядок
//     подтверждений не важен.
//
// Нормализация текста (title, профессия, путь предков): trim, схлопывание
// пробелов, lower case, ё→е, удаление пунктуации в конце. Это позволяет
// сматчить «Отчётность» и «отчетность.»
func runCompareNormalizeText(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "ё", "е")
	value = strings.TrimRight(value, ".,;:!?…»)\"'")
	return strings.TrimSpace(value)
}

// runCompareNormalizeEvidenceText — мягкая нормализация цитаты/якоря: только
// пробелы, регистр и пунктуация значимы (это содержание подтверждения).
func runCompareNormalizeEvidenceText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// runCompareNodeKey — семантический ключ узла: тип, нормализованное название и
// нормализованный путь названий предков от корня.
type runCompareNodeKey struct {
	Type       domain.NodeType
	Title      string
	ParentPath string
}

const runComparePathSeparator = "\x1f"

// runCompareResult — итог двухпроходного сравнения деревьев.
type runCompareResult struct {
	Added   []domain.Node
	Removed []domain.Node
	Changed []snapshotNodeChange
	// Match — счётчики сопоставления: сколько узлов сматчено по id, сколько
	// семантически по названию/пути и сколько осталось несопоставленными
	// (added+removed).
	Match runCompareMatch
}

// buildRunCompareResult сравнивает два дерева: сначала по id, затем
// оставшиеся — семантически. Функция чистая, не мутирует входные срезы.
func buildRunCompareResult(base, head []domain.Node) runCompareResult {
	idDiff := diffSnapshotTrees(base, head)

	basePaths := runCompareParentPaths(base)
	headPaths := runCompareParentPaths(head)

	// Ключи ищем только среди unmatched-узлов: id-совпадения уже учтены.
	headGroups := groupRunCompareNodesByKey(idDiff.Added, headPaths)
	baseGroups := groupRunCompareNodesByKey(idDiff.Removed, basePaths)

	// Однозначные пары: под ключом ровно один узел на каждой стороне.
	type candidate struct {
		base domain.Node
		head domain.Node
	}
	candidates := make([]candidate, 0, len(headGroups))
	for key, baseNodes := range baseGroups {
		if len(baseNodes) != 1 {
			continue
		}
		headNodes, exists := headGroups[key]
		if !exists || len(headNodes) != 1 {
			continue
		}
		candidates = append(candidates, candidate{base: baseNodes[0], head: headNodes[0]})
	}
	// Детерминированный порядок: по id базового узла.
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].base.ID < candidates[j].base.ID })

	changed := make([]snapshotNodeChange, 0, len(idDiff.Changed)+len(candidates))
	changed = append(changed, idDiff.Changed...)
	matchedBase := make(map[string]bool, len(candidates))
	matchedHead := make(map[string]bool, len(candidates))
	for _, pair := range candidates {
		matchedBase[pair.base.ID] = true
		matchedHead[pair.head.ID] = true
		fields := runCompareSemanticChangedFields(pair.base, pair.head, basePaths[pair.base.ID], headPaths[pair.head.ID])
		if len(fields) == 0 {
			continue
		}
		// id изменённой semantic-пары — идентификатор head-узла (текущий
		// прогон); before.id — идентификатор узла базового прогона.
		changed = append(changed, snapshotNodeChange{ID: pair.head.ID, Before: pair.base, After: pair.head, Fields: fields})
	}

	added := make([]domain.Node, 0, len(idDiff.Added))
	for _, node := range idDiff.Added {
		if !matchedHead[node.ID] {
			added = append(added, node)
		}
	}
	removed := make([]domain.Node, 0, len(idDiff.Removed))
	for _, node := range idDiff.Removed {
		if !matchedBase[node.ID] {
			removed = append(removed, node)
		}
	}

	sort.Slice(added, func(i, j int) bool { return added[i].ID < added[j].ID })
	sort.Slice(removed, func(i, j int) bool { return removed[i].ID < removed[j].ID })
	sort.SliceStable(changed, func(i, j int) bool { return changed[i].ID < changed[j].ID })

	return runCompareResult{
		Added:   added,
		Removed: removed,
		Changed: changed,
		Match: runCompareMatch{
			// id-совпадения — это все узлы базы, которые не остались removed.
			ByID:      len(base) - len(idDiff.Removed),
			ByTitle:   len(candidates),
			Unmatched: len(added) + len(removed),
		},
	}
}

// runCompareSemanticChangedFields перечисляет изменившиеся поля semantic-пары в
// фиксированном порядке title, description, type, parent_id, evidence.
func runCompareSemanticChangedFields(before, after domain.Node, beforeParentPath, afterParentPath string) []string {
	fields := make([]string, 0, 5)
	if before.Title != after.Title {
		fields = append(fields, "title")
	}
	if before.Description != after.Description {
		fields = append(fields, "description")
	}
	if before.Type != after.Type {
		fields = append(fields, "type")
	}
	if beforeParentPath != afterParentPath {
		fields = append(fields, "parent_id")
	}
	if !runCompareSameSemanticEvidence(before.Evidence, after.Evidence) {
		fields = append(fields, "evidence")
	}
	return fields
}

// runCompareSameSemanticEvidence сравнивает подтверждения semantic-пары без
// технических идентификаторов прогона: по (ref_id, quote, status), игнорируя
// порядок. source_id/evidence.id/node_id у независимых прогонов различаются
// всегда, поэтому в сравнение не входят; сами ссылки на источник остаются в
// before/after, которые возвращаются в ответе.
func runCompareSameSemanticEvidence(before, after []domain.Evidence) bool {
	if len(before) != len(after) {
		return false
	}
	beforeKeys := runCompareEvidenceKeys(before)
	afterKeys := runCompareEvidenceKeys(after)
	for index := range beforeKeys {
		if beforeKeys[index] != afterKeys[index] {
			return false
		}
	}
	return true
}

func runCompareEvidenceKeys(evidence []domain.Evidence) []string {
	keys := make([]string, 0, len(evidence))
	for _, item := range evidence {
		keys = append(keys, strings.Join([]string{
			runCompareNormalizeEvidenceText(item.RefID),
			runCompareNormalizeEvidenceText(item.Quote),
			string(snapshotEvidenceStatus(item.Status)),
		}, runComparePathSeparator))
	}
	sort.Strings(keys)
	return keys
}

// groupRunCompareNodesByKey индексирует узлы по семантическому ключу.
func groupRunCompareNodesByKey(nodes []domain.Node, parentPaths map[string]string) map[runCompareNodeKey][]domain.Node {
	groups := make(map[runCompareNodeKey][]domain.Node, len(nodes))
	for _, node := range nodes {
		key := runCompareNodeKey{
			Type:       node.Type,
			Title:      runCompareNormalizeText(node.Title),
			ParentPath: parentPaths[node.ID],
		}
		groups[key] = append(groups[key], node)
	}
	return groups
}

// runCompareParentPaths возвращает для каждого узла нормализованный путь
// названий предков от корня (сам узел в путь не входит). Для корня — "".
func runCompareParentPaths(nodes []domain.Node) map[string]string {
	byID := make(map[string]domain.Node, len(nodes))
	for _, node := range nodes {
		byID[node.ID] = node
	}
	paths := make(map[string]string, len(nodes))
	for _, node := range nodes {
		paths[node.ID] = runCompareParentPath(node, byID)
	}
	return paths
}

func runCompareParentPath(node domain.Node, byID map[string]domain.Node) string {
	parts := make([]string, 0, 4)
	visited := map[string]bool{node.ID: true}
	parentID := node.ParentID
	for parentID != nil {
		parent, exists := byID[*parentID]
		if !exists || visited[parent.ID] {
			break
		}
		visited[parent.ID] = true
		parts = append(parts, runCompareNormalizeText(parent.Title))
		parentID = parent.ParentID
	}
	// Разворачиваем путь: от корня к непосредственному родителю.
	for left, right := 0, len(parts)-1; left < right; left, right = left+1, right-1 {
		parts[left], parts[right] = parts[right], parts[left]
	}
	return strings.Join(parts, runComparePathSeparator)
}

// runCompareRootRoleTitle возвращает нормализованное название корня-роли
// дерева (для проверки «та же профессия» по содержимому дерева). Если
// role-корня нет, берётся любой корень; при нескольких кандидатах выбор
// детерминирован (минимальный id).
func runCompareRootRoleTitle(nodes []domain.Node) string {
	var candidates []domain.Node
	for _, node := range nodes {
		if node.ParentID == nil && node.Type == domain.NodeRole {
			candidates = append(candidates, node)
		}
	}
	if len(candidates) == 0 {
		for _, node := range nodes {
			if node.ParentID == nil {
				candidates = append(candidates, node)
			}
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	return runCompareNormalizeText(candidates[0].Title)
}

// runCompareSameProfession проверяет, что прогоны относятся к одной профессии.
// Учитывается либо нормализованное поле runs.profession, либо нормализованное
// название корневого role-узла: независимые прогоны одной профессии могут
// называть её по-разному («Аналитик данных» / «Data Analyst»), но строят
// дерево с одинаковым корнем.
func runCompareSameProfession(run, against *domain.Run, headNodes, baseNodes []domain.Node) bool {
	if run == nil || against == nil {
		return false
	}
	headProfession := runCompareNormalizeText(run.Profession)
	baseProfession := runCompareNormalizeText(against.Profession)
	if headProfession != "" && headProfession == baseProfession {
		return true
	}
	headRoot := runCompareRootRoleTitle(headNodes)
	baseRoot := runCompareRootRoleTitle(baseNodes)
	return headRoot != "" && headRoot == baseRoot
}
