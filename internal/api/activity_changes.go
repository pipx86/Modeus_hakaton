package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"unicode"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/logging"
)

// Карточка 31 плана MVP: отчёт об изменениях профессиональной деятельности
// при повторном исследовании.
//
// Контракт:
//
//	GET /api/v1/runs/{id}/activity-changes?against={run_id}
//
// `id` — текущее исследование, `against` — исследование, с которым сравнивают.
// Оба прогона обязаны быть доступны тому же пользователю, что и интервью
// (s.allowInterviewAccess): администратор или владелец исследования.
//
// Ответ:
//
//	{
//	  "run_id": "…", "against_run_id": "…",
//	  "same_profession": true,
//	  "added":   [activityChangeNode, …],
//	  "removed": [activityChangeNode, …],
//	  "changed": [activityChangeNode, …],
//	  "summary": {"added": 0, "removed": 0, "changed": 0,
//	              "sources_added": 0, "sources_removed": 0}
//	}
//
// Узел отчёта:
//
//	{"node_id": "…", "node_title": "…", "node_type": "task",
//	 "parent_title": "…" | null, "evidence_count": 2,
//	 "new_sources": [{"source_id": "…", "source_title": "…",
//	                  "source_type": "interview"}],
//	 "changed_fields": ["title"]} // только у changed
//
// Матчинг узлов — семантический, а не по id: у двух независимых исследований
// узлы имеют разные идентификаторы, поэтому сравниваются тип узла,
// нормализованное название и нормализованный путь предков. Хелперы
// (normalizeActivityTitle, activitySemanticKey) вынесены отдельно, чтобы тот же
// ключ могли переиспользовать другие отчёты.
//
// При разных профессиях отчёт всё равно строится: same_profession = false —
// это пометка для методиста, а не ошибка. Если у любого из исследований нет
// дерева, ответ 409 с кодом TREE_MISSING: сравнивать нечего, и пустой отчёт
// выглядел бы как «изменений нет».
//
// Коды ответа:
//   - 200 — отчёт построен (в том числе для одинаковых исследований);
//   - 400 — параметр against отсутствует или не является UUID;
//   - 401 — нет/невалидный токен (middleware);
//   - 403 — роль не входит в набор или прогон принадлежит другому пользователю;
//   - 404 — исследование или противник не найдены;
//   - 409 — у одного из исследований нет дерева деятельности (TREE_MISSING);
//   - 500 — ошибка БД;
//   - 501 — хранилище не поддерживает отчёт (in-memory Store).
//
// Отчёт только читает данные: он не меняет узлы, обоснования и не принимает
// решений за методиста.

// errActivityTreeMissing — у одного из сравниваемых исследований нет дерева.
var errActivityTreeMissing = errors.New("TREE_MISSING")

// activityChangeSource — новый материал, который подтверждает узел. Материал
// считается новым на уровне исследования: он не встречается в обоснованиях
// дерева противника вовсе.
type activityChangeSource struct {
	SourceID    string            `json:"source_id"`
	SourceTitle string            `json:"source_title"`
	SourceType  domain.SourceType `json:"source_type"`
}

// activityChangeNode — узел в одной из категорий отчёта. ChangedFields
// заполняется только для changed и перечисляет изменившиеся поля в
// фиксированном порядке title, description, type. ParentTitle — null у корня.
type activityChangeNode struct {
	NodeID        string                 `json:"node_id"`
	NodeTitle     string                 `json:"node_title"`
	NodeType      domain.NodeType        `json:"node_type"`
	ParentTitle   *string                `json:"parent_title"`
	EvidenceCount int                    `json:"evidence_count"`
	NewSources    []activityChangeSource `json:"new_sources"`
	ChangedFields []string               `json:"changed_fields,omitempty"`
}

// activityChangeSummary — счётчики отчёта. sources_added/sources_removed
// считают уникальные материалы всего дерева, а не пары «узел-материал».
type activityChangeSummary struct {
	Added          int `json:"added"`
	Removed        int `json:"removed"`
	Changed        int `json:"changed"`
	SourcesAdded   int `json:"sources_added"`
	SourcesRemoved int `json:"sources_removed"`
}

// activityChangeReport — ответ GET /runs/{id}/activity-changes.
type activityChangeReport struct {
	RunID          string                `json:"run_id"`
	AgainstRunID   string                `json:"against_run_id"`
	SameProfession bool                  `json:"same_profession"`
	Added          []activityChangeNode  `json:"added"`
	Removed        []activityChangeNode  `json:"removed"`
	Changed        []activityChangeNode  `json:"changed"`
	Summary        activityChangeSummary `json:"summary"`
}

// activityChangeEvidence — обоснование узла вместе с подписями источника.
// Название и тип источника читаются сразу, чтобы отчёт не ходил в справочник
// повторно.
type activityChangeEvidence struct {
	ID          string
	SourceID    string
	SourceTitle string
	SourceType  domain.SourceType
}

// activityChangeTreeNode — узел дерева и его обоснования.
type activityChangeTreeNode struct {
	ID          string
	ParentID    string
	Type        domain.NodeType
	Title       string
	Description string
	Evidence    []activityChangeEvidence
}

// activityChangesTreeQuery читает профессию исследования и всё его дерево
// одним запросом: LEFT JOIN сохраняет строку прогона даже без узлов, поэтому
// по ней же отличается «исследования нет» (ноль строк) от «дерева нет»
// (строка с пустым node_id). Строки обоснований идут в порядке e.id, чтобы
// evidence_count и new_sources не зависели от порядка вставки.
const activityChangesTreeQuery = `SELECT r.profession, COALESCE(n.id::text, ''), COALESCE(n.parent_id::text, ''),
       COALESCE(n.type, ''), COALESCE(n.title, ''), COALESCE(n.description, ''),
       COALESCE(e.id::text, ''), COALESCE(s.id::text, ''), COALESCE(s.title, ''), COALESCE(s.type, '')
FROM runs r
LEFT JOIN nodes n ON n.run_id = r.id
LEFT JOIN evidence e ON e.node_id = n.id
LEFT JOIN sources s ON s.id = e.source_id
WHERE r.id = $1
ORDER BY n.level, n.id, e.id`

// normalizeActivityTitle приводит название к сравнимому виду: нижний регистр,
// «ё» → «е», вся пунктуация становится пробелом, пробелы схлопываются.
// Этого достаточно, чтобы «Писать SQL-запросы» и «писать sql запросы»
// считались одним и тем же названием, но разные по смыслу названия не
// склеивались.
func normalizeActivityTitle(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "ё", "е")
	var builder strings.Builder
	builder.Grow(len(value))
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			continue
		}
		builder.WriteRune(' ')
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

// activitySemanticKey — ключ сопоставления узлов двух независимых деревьев:
// тип + нормализованное название + нормализованный путь предков. Разделитель
// \x1f не может встретиться в нормализованном названии, поэтому коллизии
// «название + путь» исключены.
func activitySemanticKey(nodeType domain.NodeType, title, ancestorPath string) string {
	return string(nodeType) + "\x1f" + normalizeActivityTitle(title) + "\x1f" + ancestorPath
}

// activityNodesByID индексирует узлы дерева по id.
func activityNodesByID(nodes []activityChangeTreeNode) map[string]activityChangeTreeNode {
	byID := make(map[string]activityChangeTreeNode, len(nodes))
	for _, node := range nodes {
		byID[node.ID] = node
	}
	return byID
}

// activityAncestorPath собирает нормализованный путь предков от корня к
// родителю узла. Цикл в повреждённых данных обрывается, а не зацикливает отчёт.
func activityAncestorPath(byID map[string]activityChangeTreeNode, nodeID string) string {
	parts := []string{}
	seen := map[string]struct{}{nodeID: {}}
	parentID := byID[nodeID].ParentID
	for parentID != "" {
		if _, loop := seen[parentID]; loop {
			break
		}
		seen[parentID] = struct{}{}
		parent, exists := byID[parentID]
		if !exists {
			break
		}
		if title := normalizeActivityTitle(parent.Title); title != "" {
			parts = append(parts, title)
		}
		parentID = parent.ParentID
	}
	for left, right := 0, len(parts)-1; left < right; left, right = left+1, right-1 {
		parts[left], parts[right] = parts[right], parts[left]
	}
	return strings.Join(parts, "\x1f")
}

// activityNodeIndex группирует индексы узлов по семантическому ключу.
// Несколько узлов с одинаковым ключом сохраняются все: при сравнении они
// сопоставляются по порядку, а лишние честно попадают в added/removed.
func activityNodeIndex(nodes []activityChangeTreeNode) map[string][]int {
	byID := activityNodesByID(nodes)
	index := make(map[string][]int, len(nodes))
	for i, node := range nodes {
		key := activitySemanticKey(node.Type, node.Title, activityAncestorPath(byID, node.ID))
		index[key] = append(index[key], i)
	}
	return index
}

// activitySourceSet собирает уникальные материалы, процитированные деревом.
func activitySourceSet(nodes []activityChangeTreeNode) map[string]struct{} {
	sources := make(map[string]struct{})
	for _, node := range nodes {
		for _, evidence := range node.Evidence {
			if evidence.SourceID != "" {
				sources[evidence.SourceID] = struct{}{}
			}
		}
	}
	return sources
}

// activitySourceDescriptors собирает подписи источников из обоснований дерева.
// Один и тот же источник может встречаться в нескольких цитатах — берём первую
// встреченную подпись, она одинакова для всех обоснований.
func activitySourceDescriptors(nodes []activityChangeTreeNode) map[string]activityChangeSource {
	descriptors := make(map[string]activityChangeSource)
	for _, node := range nodes {
		for _, evidence := range node.Evidence {
			if evidence.SourceID == "" {
				continue
			}
			if _, exists := descriptors[evidence.SourceID]; exists {
				continue
			}
			descriptors[evidence.SourceID] = activityChangeSource{
				SourceID:    evidence.SourceID,
				SourceTitle: evidence.SourceTitle,
				SourceType:  evidence.SourceType,
			}
		}
	}
	return descriptors
}

// activityParentTitle возвращает название родителя узла или null для корня.
func activityParentTitle(byID map[string]activityChangeTreeNode, node activityChangeTreeNode) *string {
	if node.ParentID == "" {
		return nil
	}
	parent, exists := byID[node.ParentID]
	if !exists || strings.TrimSpace(parent.Title) == "" {
		return nil
	}
	title := parent.Title
	return &title
}

// activityNewSources оставляет материалы узла, которых нет в дереве противника,
// и сортирует их по названию и id — ответ не зависит от порядка обоснований.
func activityNewSources(node activityChangeTreeNode, againstSources map[string]struct{}, descriptors map[string]activityChangeSource) []activityChangeSource {
	newSources := []activityChangeSource{}
	seen := make(map[string]struct{})
	for _, evidence := range node.Evidence {
		if evidence.SourceID == "" {
			continue
		}
		if _, known := againstSources[evidence.SourceID]; known {
			continue
		}
		if _, duplicate := seen[evidence.SourceID]; duplicate {
			continue
		}
		seen[evidence.SourceID] = struct{}{}
		descriptor, exists := descriptors[evidence.SourceID]
		if !exists {
			descriptor = activityChangeSource{
				SourceID:    evidence.SourceID,
				SourceTitle: evidence.SourceTitle,
				SourceType:  evidence.SourceType,
			}
		}
		newSources = append(newSources, descriptor)
	}
	sort.Slice(newSources, func(i, j int) bool {
		if newSources[i].SourceTitle != newSources[j].SourceTitle {
			return newSources[i].SourceTitle < newSources[j].SourceTitle
		}
		return newSources[i].SourceID < newSources[j].SourceID
	})
	return newSources
}

// activityChangedFields перечисляет изменившиеся поля узла в порядке контракта
// (title, description, type). Сравнение пути и обоснований сюда не входит:
// для них у отчёта есть отдельные категории и new_sources.
func activityChangedFields(before, after activityChangeTreeNode) []string {
	fields := make([]string, 0, 3)
	if before.Title != after.Title {
		fields = append(fields, "title")
	}
	if before.Description != after.Description {
		fields = append(fields, "description")
	}
	if before.Type != after.Type {
		fields = append(fields, "type")
	}
	return fields
}

// activityChangeTypeRank задаёт человекочитаемый порядок узлов в отчёте:
// роли, функции, задачи, действия.
func activityChangeTypeRank(nodeType domain.NodeType) int {
	switch nodeType {
	case domain.NodeRole:
		return 0
	case domain.NodeFunction:
		return 1
	case domain.NodeTask:
		return 2
	case domain.NodeAction:
		return 3
	default:
		return 4
	}
}

// sortActivityChangeNodes делает порядок узлов детерминированным: уровень
// дерева, нормализованное название, затем id (у разных исследований id разный).
func sortActivityChangeNodes(nodes []activityChangeNode) {
	sort.Slice(nodes, func(i, j int) bool {
		left, right := nodes[i], nodes[j]
		if activityChangeTypeRank(left.NodeType) != activityChangeTypeRank(right.NodeType) {
			return activityChangeTypeRank(left.NodeType) < activityChangeTypeRank(right.NodeType)
		}
		leftTitle, rightTitle := normalizeActivityTitle(left.NodeTitle), normalizeActivityTitle(right.NodeTitle)
		if leftTitle != rightTitle {
			return leftTitle < rightTitle
		}
		return left.NodeID < right.NodeID
	})
}

// sameActivityProfession сравнивает профессии по той же нормализации, что и
// названия узлов: регистр, «ё» и лишние пробелы не должны давать ложную
// пометку «профессии различаются».
func sameActivityProfession(left, right string) bool {
	return normalizeActivityTitle(left) == normalizeActivityTitle(right)
}

// activityChangeNodeFrom собирает элемент отчёта для узла текущего дерева.
func activityChangeNodeFrom(node activityChangeTreeNode, byID map[string]activityChangeTreeNode, againstSources map[string]struct{}, descriptors map[string]activityChangeSource) activityChangeNode {
	return activityChangeNode{
		NodeID:        node.ID,
		NodeTitle:     node.Title,
		NodeType:      node.Type,
		ParentTitle:   activityParentTitle(byID, node),
		EvidenceCount: len(node.Evidence),
		NewSources:    activityNewSources(node, againstSources, descriptors),
	}
}

// buildActivityChanges сравнивает два уже прочитанных дерева. Функция чистая:
// в базу не ходит, не мутирует входные срезы и не зависит от порядка строк,
// кроме заданного SQL (узлы идут level, id).
func buildActivityChanges(runID, againstRunID, runProfession string, runNodes []activityChangeTreeNode, againstProfession string, againstNodes []activityChangeTreeNode) activityChangeReport {
	runByID := activityNodesByID(runNodes)
	againstByID := activityNodesByID(againstNodes)
	againstIndex := activityNodeIndex(againstNodes)
	runSources := activitySourceSet(runNodes)
	againstSources := activitySourceSet(againstNodes)
	runDescriptors := activitySourceDescriptors(runNodes)

	report := activityChangeReport{
		RunID:          runID,
		AgainstRunID:   againstRunID,
		SameProfession: sameActivityProfession(runProfession, againstProfession),
		Added:          []activityChangeNode{},
		Removed:        []activityChangeNode{},
		Changed:        []activityChangeNode{},
	}
	matchedAgainst := make(map[int]struct{}, len(againstNodes))
	for _, node := range runNodes {
		key := activitySemanticKey(node.Type, node.Title, activityAncestorPath(runByID, node.ID))
		match := -1
		for _, candidate := range againstIndex[key] {
			if _, used := matchedAgainst[candidate]; used {
				continue
			}
			match = candidate
			break
		}
		if match < 0 {
			report.Added = append(report.Added, activityChangeNodeFrom(node, runByID, againstSources, runDescriptors))
			continue
		}
		matchedAgainst[match] = struct{}{}
		before := againstNodes[match]
		fields := activityChangedFields(before, node)
		if len(fields) == 0 {
			continue
		}
		changed := activityChangeNodeFrom(node, runByID, againstSources, runDescriptors)
		changed.ChangedFields = fields
		report.Changed = append(report.Changed, changed)
	}
	for i, node := range againstNodes {
		if _, used := matchedAgainst[i]; used {
			continue
		}
		// У исчезнувшего узла новых материалов быть не может: new_sources
		// перечисляет только то, что подтверждает текущее дерево.
		report.Removed = append(report.Removed, activityChangeNode{
			NodeID:        node.ID,
			NodeTitle:     node.Title,
			NodeType:      node.Type,
			ParentTitle:   activityParentTitle(againstByID, node),
			EvidenceCount: len(node.Evidence),
			NewSources:    []activityChangeSource{},
		})
	}
	sortActivityChangeNodes(report.Added)
	sortActivityChangeNodes(report.Removed)
	sortActivityChangeNodes(report.Changed)
	report.Summary = activityChangeSummary{
		Added:          len(report.Added),
		Removed:        len(report.Removed),
		Changed:        len(report.Changed),
		SourcesAdded:   countActivitySourcesOnlyIn(runSources, againstSources),
		SourcesRemoved: countActivitySourcesOnlyIn(againstSources, runSources),
	}
	return report
}

// countActivitySourcesOnlyIn считает материалы, которые есть в left и которых
// нет в right. Используется для summary.sources_added/sources_removed.
func countActivitySourcesOnlyIn(left, right map[string]struct{}) int {
	count := 0
	for sourceID := range left {
		if _, exists := right[sourceID]; !exists {
			count++
		}
	}
	return count
}

// activityChangesTree читает профессию исследования и его дерево. Пустой
// результат означает, что исследование не найдено; дерево без узлов
// возвращается как пустой срез и обрабатывается отдельно (409).
func (p *PGStore) activityChangesTree(ctx context.Context, runID string) (string, []activityChangeTreeNode, error) {
	rows, err := p.Pool.Query(ctx, activityChangesTreeQuery, runID)
	if err != nil {
		return "", nil, translate(err)
	}
	defer rows.Close()
	profession := ""
	nodes := []activityChangeTreeNode{}
	byID := make(map[string]int)
	sawRow := false
	for rows.Next() {
		sawRow = true
		var rowProfession, nodeID, parentID, nodeType, title, description string
		var evidenceID, sourceID, sourceTitle, sourceType string
		if err := rows.Scan(&rowProfession, &nodeID, &parentID, &nodeType, &title, &description,
			&evidenceID, &sourceID, &sourceTitle, &sourceType); err != nil {
			return "", nil, err
		}
		if profession == "" {
			profession = rowProfession
		}
		if nodeID == "" {
			continue
		}
		index, exists := byID[nodeID]
		if !exists {
			index = len(nodes)
			byID[nodeID] = index
			nodes = append(nodes, activityChangeTreeNode{
				ID:          nodeID,
				ParentID:    parentID,
				Type:        domain.NodeType(nodeType),
				Title:       title,
				Description: description,
			})
		}
		if evidenceID == "" || sourceID == "" {
			continue
		}
		nodes[index].Evidence = append(nodes[index].Evidence, activityChangeEvidence{
			ID:          evidenceID,
			SourceID:    sourceID,
			SourceTitle: sourceTitle,
			SourceType:  domain.SourceType(sourceType),
		})
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	if !sawRow {
		return "", nil, ErrNotFound
	}
	return profession, nodes, nil
}

// RunActivityChanges строит отчёт об изменениях деятельности между двумя
// исследованиями (карточка 31). Оба дерева читаются из nodes/evidence: отчёт
// показывает текущее состояние, а не выбранный снимок, потому что вопрос
// методиста — «что изменилось в деятельности», а не «что изменилось в файле
// снимка».
func (p *PGStore) RunActivityChanges(runID, againstRunID string) (*activityChangeReport, error) {
	if p == nil || p.Pool == nil {
		return nil, ErrServiceUnavailable
	}
	if !validUUID(runID) || !validUUID(againstRunID) {
		return nil, ErrNotFound
	}
	ctx, cancel := operation()
	defer cancel()
	runProfession, runNodes, err := p.activityChangesTree(ctx, runID)
	if err != nil {
		return nil, err
	}
	if len(runNodes) == 0 {
		return nil, errActivityTreeMissing
	}
	againstProfession, againstNodes, err := p.activityChangesTree(ctx, againstRunID)
	if err != nil {
		return nil, err
	}
	if len(againstNodes) == 0 {
		return nil, errActivityTreeMissing
	}
	report := buildActivityChanges(runID, againstRunID, runProfession, runNodes, againstProfession, againstNodes)
	return &report, nil
}

// writeActivityChangesError переводит ошибки отчёта в HTTP-ответы.
func writeActivityChangesError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "исследование не найдено")
	case errors.Is(err, errActivityTreeMissing):
		writeErr(w, http.StatusConflict, "TREE_MISSING", "у исследования ещё нет дерева деятельности — сравнивать пока нечего")
	case errors.Is(err, ErrServiceUnavailable):
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "для отчёта об изменениях требуется PostgreSQL")
	default:
		logging.Logger.Error("activity_changes_failed", "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось построить отчёт об изменениях деятельности")
	}
}

// handleActivityChanges godoc
// @Summary      Changes of professional activity between two research runs
// @Description  Compares the current activity trees of two runs of the same owner. Nodes are matched semantically by type, normalized title and normalized ancestor path, because independent runs never share node ids. Returns added, removed and changed nodes with parent title, evidence count and the new sources that confirm the node; summary reports counts per category and the number of unique sources added or removed. Different professions are not an error: same_profession=false marks the report. A run without a tree is 409 TREE_MISSING. Read-only card 31 of the MVP plan.
// @Tags         runs
// @Produce      json
// @Param        id       path   string  true  "Run ID"
// @Param        against  query  string  true  "Run ID to compare with"
// @Success      200  {object}  activityChangeReport
// @Failure      400  {object}  errorBody  "against is missing or not a UUID"
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      409  {object}  errorBody  "one of the runs has no activity tree (TREE_MISSING)"
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/activity-changes [get]
func (s *Server) handleActivityChanges(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser, auth.RoleSpecialist) {
		return
	}
	// Параметр проверяем до обращения к хранилищу: невалидный запрос — это
	// 400 при любом бэкенде, в том числе когда отчёт не поддерживается.
	against := strings.TrimSpace(r.URL.Query().Get("against"))
	if against == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "параметр against обязателен")
		return
	}
	if !validUUID(against) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "against должен быть UUID исследования")
		return
	}
	runID := r.PathValue("id")
	if !s.allowInterviewAccess(w, r, runID) {
		return
	}
	// Доступ проверяется к обоим прогонам: отчёт раскрывает дерево противника,
	// поэтому чужое исследование не должно даже подтверждать своё существование.
	if !s.allowInterviewAccess(w, r, against) {
		return
	}
	pg, ok := requirePG(w, s)
	if !ok {
		return
	}
	report, err := pg.RunActivityChanges(runID, against)
	if err != nil {
		writeActivityChangesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}
