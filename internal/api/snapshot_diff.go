package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
)

// Snapshot-to-snapshot tree diff.
//
// Контракт:
//
//	GET /api/v1/runs/{id}/snapshots/{head_snapshot_id}/diff?against={base_snapshot_id}
//
// `head_snapshot_id` в пути — «голова» сравнения (head, новое состояние),
// параметр `against` — «база» (base, состояние, с которым сравнивают). Оба
// снимка обязаны принадлежать исследованию `{id}`.
//
// Ответ:
//
//	{
//	  "run_id": "…",
//	  "base":  {"id": "…", "version": 1},
//	  "head":  {"id": "…", "version": 2},
//	  "added":   [domain.Node, …],   // узлы только в head, отсортированы по id
//	  "removed": [domain.Node, …],   // узлы только в base, отсортированы по id
//	  "changed": [{"id": "…", "before": domain.Node, "after": domain.Node,
//	               "fields": ["title", "description", "evidence"],
//	               "evidence_added":   [{"id": "…", "quote": "…", "source_id": "…"}],
//	               "evidence_removed": []}],
//	  "summary": {"added": 0, "removed": 0, "changed": 1},
//	  "story": "Узлов добавлено: 0, удалено: 0, изменено: 1. Изменён узел «Отчёт»: подтверждения.",
//	  "story_lines": ["Узлов добавлено: 0, удалено: 0, изменено: 1.", "Изменён узел «Отчёт»: подтверждения."]
//	}
//
// `story`/`story_lines` — детерминированное человекочитаемое описание diff'а
// на русском языке (см. comparison_story.go). Количества в заголовке истории
// всегда совпадают с `summary`; подробности идут в порядке added → removed →
// changed, внутри категории — по id узла, поля изменения — в порядке
// title, description, type, parent_id, evidence; длина строки ограничена.
//
// Узлы и подтверждения возвращаются в том же виде, что и в дереве
// исследования (domain.Node / domain.Evidence), поэтому у каждого evidence
// есть source_id, ref_id и quote — по ним UI открывает исходную цитату
// (GET /api/v1/sources/{source_id} — источник, ref_id — якорь фрагмента).
//
// Сравнение:
//   - узлы сопоставляются по id;
//   - changed перечисляет изменённые поля в фиксированном порядке:
//     title, description, type, parent_id, evidence;
//   - для evidence сравниваются id, node_id, source_id, ref_id, quote, status;
//     порядок подтверждений внутри узла не считается изменением;
//   - status «pending» по умолчанию нормализуется (пустой status == pending);
//   - в каждом элементе changed есть аддитивные evidence_added и evidence_removed
//     (карточка 19): обоснования, которых не было в base и которых больше нет в
//     head. Сравнение по id, порядок не важен; правка цитаты под тем же id
//     попадает в fields = ["evidence"], но не в эти списки.
//
// Коды ответа:
//   - 200 — diff построен (в том числе для одинаковых снимков — пустой diff);
//   - 400 — параметр against отсутствует или не является UUID;
//   - 401 — нет/невалидный токен (middleware);
//   - 403 — роль не входит в набор чтения снимков;
//   - 404 — исследование или любой из снимков не найден;
//   - 409 — снимок существует, но принадлежит другому исследованию;
//   - 500 — снимок повреждён (не удалось разобрать дерево) или ошибка БД;
//   - 501 — хранилище не поддерживает снимки (in-memory Store).
//
// Права: как у GET /api/v1/runs/{id}/snapshots/{snapshot_id}
// (admin, teacher, rop, guest). Роль student намеренно не добавлена, чтобы не
// расширять RBAC сверх матрицы снимков, задокументированной в SECURITY_REVIEW.md.

var (
	// ErrSnapshotForeignRun — снимок найден, но относится к другому run.
	ErrSnapshotForeignRun = errors.New("SNAPSHOT_RUN_MISMATCH")
	// errSnapshotTreeInvalid — сохранённый JSON снимка не удалось разобрать.
	errSnapshotTreeInvalid = errors.New("SNAPSHOT_TREE_INVALID")
)

// snapshotDiffer — узкий интерфейс, который нужен HTTP-хендлеру. Его
// реализует *PGStore; в unit-тестах подменяется заглушкой.
type snapshotDiffer interface {
	snapshotDiff(ctx context.Context, runID, headID, baseID string) (snapshotDiffResponse, error)
}

type snapshotVersionRef struct {
	ID      string `json:"id"`
	Version int32  `json:"version"`
}

type snapshotNodeChange struct {
	ID     string      `json:"id"`
	Before domain.Node `json:"before"`
	After  domain.Node `json:"after"`
	Fields []string    `json:"fields"`
	// Карточка 19 плана MVP: какие обоснования появились и исчезли у узла.
	// Поля аддитивные — before/after и fields остаются как есть, поэтому
	// существующий интерфейс не ломается. Сравнение идёт по id обоснования:
	// правка цитаты под тем же id — изменение (fields содержит evidence), а не
	// добавление и удаление. Срезы всегда не-nil, чтобы JSON отдавал [].
	EvidenceAdded   []snapshotEvidenceRef `json:"evidence_added"`
	EvidenceRemoved []snapshotEvidenceRef `json:"evidence_removed"`
}

// snapshotEvidenceRef — компактная ссылка на обоснование в diff'е (карточка 19):
// id, цитата и источник, по которым интерфейс открывает исходный материал.
type snapshotEvidenceRef struct {
	ID       string `json:"id"`
	Quote    string `json:"quote"`
	SourceID string `json:"source_id"`
}

type snapshotDiffSummary struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Changed int `json:"changed"`
}

type snapshotDiffResponse struct {
	RunID   string               `json:"run_id"`
	Base    snapshotVersionRef   `json:"base"`
	Head    snapshotVersionRef   `json:"head"`
	Added   []domain.Node        `json:"added"`
	Removed []domain.Node        `json:"removed"`
	Changed []snapshotNodeChange `json:"changed"`
	Summary snapshotDiffSummary  `json:"summary"`
	// Story — одна строка человекочитаемого описания diff'а на русском языке.
	// Строится хендлером из тех же Added/Removed/Changed через общий с
	// run-to-run сравнением helper buildComparisonStory (comparison_story.go).
	Story string `json:"story"`
	// StoryLines — то же описание построчно: заголовок с количествами и по
	// строке на каждое изменение; длинные истории обрезаются с пометкой
	// «Ещё изменений: N.».
	StoryLines []string `json:"story_lines"`
}

// snapshotTreeDocument — формат столбца snapshots.tree (см.
// db/queries/snapshots.sql, BuildSnapshotTree).
type snapshotTreeDocument struct {
	SchemaVersion int                        `json:"schema_version"`
	RootNodeID    string                     `json:"root_node_id"`
	Nodes         []snapshotTreeDocumentNode `json:"nodes"`
}

type snapshotTreeDocumentNode struct {
	ID          string                         `json:"id"`
	ParentID    *string                        `json:"parent_id"`
	Level       int                            `json:"level"`
	Type        string                         `json:"type"`
	Title       string                         `json:"title"`
	Description *string                        `json:"description"`
	Evidence    []snapshotTreeDocumentEvidence `json:"evidence"`
}

// snapshotTreeDocumentEvidence описывает и старый «вложенный» формат снимка
// (source + approved), и возможный плоский формат (source_id + status): парсер
// поддерживает оба, чтобы не зависеть от версии схемы конкретного снимка.
type snapshotTreeDocumentEvidence struct {
	ID          string                      `json:"id"`
	NodeID      string                      `json:"node_id"`
	SourceID    string                      `json:"source_id"`
	RefID       *string                     `json:"ref_id"`
	Quote       string                      `json:"quote"`
	Approved    bool                        `json:"approved"`
	Status      string                      `json:"status"`
	ClaimOrigin string                      `json:"claim_origin"`
	InterviewID string                      `json:"interview_id"`
	Source      *snapshotTreeDocumentSource `json:"source"`
}

type snapshotTreeDocumentSource struct {
	ID        string  `json:"id"`
	Type      string  `json:"type"`
	Title     string  `json:"title"`
	SourceURL *string `json:"source_url"`
}

// parseSnapshotTree разбирает JSON сохранённого снимка в плоский список узлов
// дерева. Функция чистая: ошибки означают, что снимок повреждён.
func parseSnapshotTree(raw json.RawMessage) ([]domain.Node, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, fmt.Errorf("%w: пустое дерево", errSnapshotTreeInvalid)
	}
	var document snapshotTreeDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("%w: %v", errSnapshotTreeInvalid, err)
	}
	nodes := make([]domain.Node, 0, len(document.Nodes))
	seenNodes := make(map[string]struct{}, len(document.Nodes))
	for index, item := range document.Nodes {
		if strings.TrimSpace(item.ID) == "" {
			return nil, fmt.Errorf("%w: узел #%d без идентификатора", errSnapshotTreeInvalid, index)
		}
		if _, duplicate := seenNodes[item.ID]; duplicate {
			return nil, fmt.Errorf("%w: повторяется узел %s", errSnapshotTreeInvalid, item.ID)
		}
		seenNodes[item.ID] = struct{}{}
		node := domain.Node{
			ID:       item.ID,
			Type:     domain.NodeType(item.Type),
			Title:    item.Title,
			ParentID: item.ParentID,
			Evidence: make([]domain.Evidence, 0, len(item.Evidence)),
		}
		if item.Description != nil {
			node.Description = *item.Description
		}
		seenEvidence := make(map[string]struct{}, len(item.Evidence))
		for _, rawEvidence := range item.Evidence {
			if strings.TrimSpace(rawEvidence.ID) == "" {
				return nil, fmt.Errorf("%w: у узла %s подтверждение без идентификатора", errSnapshotTreeInvalid, item.ID)
			}
			if _, duplicate := seenEvidence[rawEvidence.ID]; duplicate {
				return nil, fmt.Errorf("%w: у узла %s повторяется подтверждение %s", errSnapshotTreeInvalid, item.ID, rawEvidence.ID)
			}
			seenEvidence[rawEvidence.ID] = struct{}{}
			node.Evidence = append(node.Evidence, presentSnapshotEvidence(item.ID, rawEvidence))
		}
		sortSnapshotEvidence(node.Evidence)
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// presentSnapshotEvidence приводит evidence из JSON снимка к domain.Evidence.
// Старые снимки хранят статус как approved=true/false, новые могут хранить
// status напрямую; оба варианта дают одинаковый результат.
func presentSnapshotEvidence(nodeID string, raw snapshotTreeDocumentEvidence) domain.Evidence {
	evidence := domain.Evidence{
		ID:          raw.ID,
		NodeID:      nodeID,
		SourceID:    raw.SourceID,
		RefID:       stringOrEmpty(raw.RefID),
		Quote:       raw.Quote,
		ClaimOrigin: domain.ClaimOrigin(raw.ClaimOrigin),
		InterviewID: raw.InterviewID,
	}
	if evidence.SourceID == "" && raw.Source != nil {
		evidence.SourceID = raw.Source.ID
	}
	if raw.NodeID != "" {
		evidence.NodeID = raw.NodeID
	}
	switch {
	case raw.Status != "":
		evidence.Status = domain.EvidenceStatus(raw.Status)
	case raw.Approved:
		evidence.Status = domain.EvidenceApproved
	default:
		evidence.Status = domain.EvidencePending
	}
	return evidence
}

// sortSnapshotEvidence делает порядок подтверждений детерминированным: снимки
// строятся в порядке created_at,id, но сравнение и ответ не должны зависеть от
// порядка вставки.
func sortSnapshotEvidence(evidence []domain.Evidence) {
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].ID < evidence[j].ID })
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// snapshotTreeDiff — результат чистого сравнения двух разобранных снимков.
type snapshotTreeDiff struct {
	Added   []domain.Node
	Removed []domain.Node
	Changed []snapshotNodeChange
}

// diffSnapshotTrees сравнивает два снимка по идентификаторам узлов.
// Функция чистая и не зависит от БД.
func diffSnapshotTrees(base, head []domain.Node) snapshotTreeDiff {
	baseByID := make(map[string]domain.Node, len(base))
	for _, node := range base {
		baseByID[node.ID] = node
	}
	headByID := make(map[string]domain.Node, len(head))
	for _, node := range head {
		headByID[node.ID] = node
	}
	result := snapshotTreeDiff{
		Added:   []domain.Node{},
		Removed: []domain.Node{},
		Changed: []snapshotNodeChange{},
	}
	for _, node := range head {
		before, exists := baseByID[node.ID]
		if !exists {
			result.Added = append(result.Added, node)
			continue
		}
		fields := snapshotChangedFields(before, node)
		if len(fields) > 0 {
			evidenceAdded, evidenceRemoved := diffSnapshotEvidence(before.Evidence, node.Evidence)
			result.Changed = append(result.Changed, snapshotNodeChange{
				ID:              node.ID,
				Before:          before,
				After:           node,
				Fields:          fields,
				EvidenceAdded:   evidenceAdded,
				EvidenceRemoved: evidenceRemoved,
			})
		}
	}
	for _, node := range base {
		if _, exists := headByID[node.ID]; !exists {
			result.Removed = append(result.Removed, node)
		}
	}
	sort.Slice(result.Added, func(i, j int) bool { return result.Added[i].ID < result.Added[j].ID })
	sort.Slice(result.Removed, func(i, j int) bool { return result.Removed[i].ID < result.Removed[j].ID })
	sort.Slice(result.Changed, func(i, j int) bool { return result.Changed[i].ID < result.Changed[j].ID })
	return result
}

// snapshotChangedFields возвращает имена изменившихся полей узла в
// фиксированном порядке. Пустой срез означает «узел не изменился».
func snapshotChangedFields(before, after domain.Node) []string {
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
	if !sameSnapshotParent(before.ParentID, after.ParentID) {
		fields = append(fields, "parent_id")
	}
	if !sameSnapshotEvidence(before.Evidence, after.Evidence) {
		fields = append(fields, "evidence")
	}
	return fields
}

func sameSnapshotParent(before, after *string) bool {
	if before == nil || after == nil {
		return before == nil && after == nil
	}
	return *before == *after
}

// sameSnapshotEvidence сравнивает подтверждения по id и значимым полям,
// игнорируя порядок. Сравнение включает source_id/ref_id/quote/status — именно
// эти поля UI использует для перехода к исходной цитате.
func sameSnapshotEvidence(before, after []domain.Evidence) bool {
	if len(before) != len(after) {
		return false
	}
	afterByID := make(map[string]domain.Evidence, len(after))
	for _, evidence := range after {
		afterByID[evidence.ID] = evidence
	}
	for _, evidence := range before {
		other, exists := afterByID[evidence.ID]
		if !exists {
			return false
		}
		evidence.Status = snapshotEvidenceStatus(evidence.Status)
		other.Status = snapshotEvidenceStatus(other.Status)
		if evidence != other {
			return false
		}
	}
	return true
}

// snapshotEvidenceStatus нормализует пустой статус к «pending»: старые снимки
// не содержали его вовсе, и это не должно выглядеть как изменение.
func snapshotEvidenceStatus(status domain.EvidenceStatus) domain.EvidenceStatus {
	if status == "" {
		return domain.EvidencePending
	}
	return status
}

// diffSnapshotEvidence возвращает обоснования, которые появились (added) и
// исчезли (removed) между двумя версиями узла (карточка 19). Функция чистая и
// не зависит от порядка подтверждений: результат отсортирован по id, а срезы
// всегда не-nil, чтобы JSON отдавал [] вместо null. Правка цитаты под тем же
// id — не добавление и не удаление: такой случай остаётся в before/after и в
// поле fields = ["evidence"].
func diffSnapshotEvidence(before, after []domain.Evidence) (added, removed []snapshotEvidenceRef) {
	beforeByID := make(map[string]domain.Evidence, len(before))
	for _, evidence := range before {
		beforeByID[evidence.ID] = evidence
	}
	afterByID := make(map[string]domain.Evidence, len(after))
	for _, evidence := range after {
		afterByID[evidence.ID] = evidence
	}
	added = []snapshotEvidenceRef{}
	removed = []snapshotEvidenceRef{}
	for _, evidence := range after {
		if _, exists := beforeByID[evidence.ID]; !exists {
			added = append(added, presentSnapshotEvidenceRef(evidence))
		}
	}
	for _, evidence := range before {
		if _, exists := afterByID[evidence.ID]; !exists {
			removed = append(removed, presentSnapshotEvidenceRef(evidence))
		}
	}
	sort.Slice(added, func(i, j int) bool { return added[i].ID < added[j].ID })
	sort.Slice(removed, func(i, j int) bool { return removed[i].ID < removed[j].ID })
	return added, removed
}

// presentSnapshotEvidenceRef оставляет от обоснования только поля, нужные
// карточке 19: интерфейсу не нужно целиком дублировать before/after.
func presentSnapshotEvidenceRef(evidence domain.Evidence) snapshotEvidenceRef {
	return snapshotEvidenceRef{ID: evidence.ID, Quote: evidence.Quote, SourceID: evidence.SourceID}
}

// snapshotInRun загружает снимок исследования и отличает «снимка нет» (404)
// от «снимок есть, но у другого исследования» (409).
func (p *PGStore) snapshotInRun(ctx context.Context, runID, snapshotID string) (snapshotResponse, error) {
	item, err := p.getSnapshot(ctx, runID, snapshotID)
	if err == nil {
		return item, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return snapshotResponse{}, err
	}
	if !validUUID(runID) || !validUUID(snapshotID) {
		return snapshotResponse{}, ErrNotFound
	}
	var actualRunID string
	if scanErr := p.Pool.QueryRow(ctx, `SELECT run_id::text FROM snapshots WHERE id=$1`, snapshotID).Scan(&actualRunID); scanErr != nil {
		if errors.Is(translate(scanErr), ErrNotFound) {
			return snapshotResponse{}, ErrNotFound
		}
		return snapshotResponse{}, scanErr
	}
	if actualRunID != runID {
		return snapshotResponse{}, ErrSnapshotForeignRun
	}
	return snapshotResponse{}, ErrNotFound
}

// snapshotDiff реализует контракт snapshotDiffer на PostgreSQL.
func (p *PGStore) snapshotDiff(ctx context.Context, runID, headID, baseID string) (snapshotDiffResponse, error) {
	if _, err := p.GetRun(runID); err != nil {
		return snapshotDiffResponse{}, err
	}
	head, err := p.snapshotInRun(ctx, runID, headID)
	if err != nil {
		return snapshotDiffResponse{}, err
	}
	base, err := p.snapshotInRun(ctx, runID, baseID)
	if err != nil {
		return snapshotDiffResponse{}, err
	}
	headNodes, err := parseSnapshotTree(head.Tree)
	if err != nil {
		return snapshotDiffResponse{}, err
	}
	baseNodes, err := parseSnapshotTree(base.Tree)
	if err != nil {
		return snapshotDiffResponse{}, err
	}
	treeDiff := diffSnapshotTrees(baseNodes, headNodes)
	return snapshotDiffResponse{
		RunID:   runID,
		Base:    snapshotVersionRef{ID: base.ID.String(), Version: base.Version},
		Head:    snapshotVersionRef{ID: head.ID.String(), Version: head.Version},
		Added:   treeDiff.Added,
		Removed: treeDiff.Removed,
		Changed: treeDiff.Changed,
		Summary: snapshotDiffSummary{
			Added:   len(treeDiff.Added),
			Removed: len(treeDiff.Removed),
			Changed: len(treeDiff.Changed),
		},
	}, nil
}

func snapshotDiffError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "исследование или снимок не найден")
	case errors.Is(err, ErrSnapshotForeignRun):
		writeErr(w, http.StatusConflict, "SNAPSHOT_RUN_MISMATCH", "снимок принадлежит другому исследованию")
	case errors.Is(err, errSnapshotTreeInvalid):
		writeErr(w, http.StatusInternalServerError, "SNAPSHOT_TREE_INVALID", "не удалось разобрать дерево снимка")
	default:
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось сравнить снимки")
	}
}

// handleSnapshotDiff godoc
// @Summary      Diff two snapshots of a research run
// @Description  Compares the tree stored in the `against` snapshot (base) with the tree stored in the path snapshot (head). Nodes are matched by id; returns added/removed nodes and changed nodes with the changed field list. Evidence changes are visible because every returned node carries evidence with source_id/ref_id/quote/status, so the UI can jump to the source quote. Each changed node also carries the additive evidence_added and evidence_removed arrays (card 19): evidence items present only in head or only in base, compared by id.
// @Tags         runs
// @Produce      json
// @Param        id           path   string  true  "Run ID"
// @Param        snapshot_id  path   string  true  "Head snapshot ID"
// @Param        against      query  string  true  "Base snapshot ID"
// @Success      200  {object}  snapshotDiffResponse
// @Failure      400  {object}  errorBody  "against is missing or not a UUID"
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      409  {object}  errorBody  "snapshot belongs to another run"
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/snapshots/{snapshot_id}/diff [get]
func (s *Server) handleSnapshotDiff(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser) {
		return
	}
	// Параметры проверяем до обращения к хранилищу: невалидный запрос — это
	// 400 при любом бэкенде, в том числе когда снимки не поддерживаются.
	against := strings.TrimSpace(r.URL.Query().Get("against"))
	if against == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "параметр against обязателен")
		return
	}
	if !validUUID(against) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "against должен быть UUID снимка")
		return
	}
	differ, ok := s.store.(snapshotDiffer)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "для сравнения снимков требуется PostgreSQL")
		return
	}
	result, err := differ.snapshotDiff(r.Context(), r.PathValue("id"), r.PathValue("snapshot_id"), against)
	if err != nil {
		snapshotDiffError(w, err)
		return
	}
	// Story строится на HTTP-слое, а не в хранилище: репозиторий отдаёт только
	// структурированный diff, а человекочитаемое описание собирается общим
	// helper'ом (тем же, что использует run-to-run comparison), поэтому старые
	// реализации snapshotDiffer не обязаны его знать.
	result.Story, result.StoryLines = buildComparisonStory(result.Added, result.Removed, result.Changed)
	writeJSON(w, http.StatusOK, result)
}
