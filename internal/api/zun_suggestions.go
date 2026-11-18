package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5"
)

// Auto-ZUN: чтение rule-based предложений и их применение (feature #10).
//
// Контракт:
//
//	GET  /api/v1/runs/{id}/zun/suggestions
//	POST /api/v1/runs/{id}/zun/suggestions/apply
//
// GET возвращает JSON-массив предложений в порядке узлов дерева (как отдаёт
// GET /api/v1/runs/{id}/tree). Для каждого узла:
//
//	{
//	  "node_id": "…",
//	  "node_title": "…",
//	  "knowledge": ["…"],     // пусто, если у узла уже есть ЗУН
//	  "skills": ["…"],
//	  "abilities": ["…"],
//	  "source": "rule",       // всегда "rule": предложения не от LLM
//	  "current_zun": {...} | null,
//	  "can_apply": true
//	}
//
// Правила вывода описаны в zun_rules.go. Узел с непустым ЗУН сохраняется как
// есть: предложения для него пустые, can_apply=false — правила не перезаписывают
// ни ручные, ни ранее сгенерированные значения. Узел без ЗУН всегда получает
// непустое предложение (fallback по типу узла и профессии).
//
// POST apply принимает ровно один узел:
//
//	{"node_id":"…","knowledge":["…"],"skills":["…"],"abilities":["…"]}
//
// и сохраняет ЗУН, только если сейчас он пуст. Уже существующий ЗУН не
// перезаписывается: 409 ZUN_ALREADY_SET (фронтенд перечитывает предложения).
// Пункты нормализуются (trim, схлопывание пробелов, дедупликация без учёта
// регистра, максимум 200 символов на пункт и 50 пунктов на список). Валидация
// до записи: node_id обязателен, нужен хотя бы один пункт ЗУН — иначе 400.
//
// Права: admin, teacher, rop. student/guest получают 403. Коды: 200 — массив
// предложений / применённый ЗУН; 400 — некорректный JSON или пустая полезная
// нагрузка; 404 — прогон или узел не найдены; 409 — ЗУН уже заполнен;
// 500 — ошибка БД; 501 — хранилище не поддерживает сохранение ЗУН.

// ErrZunAlreadySet — у узла уже есть непустой ЗУН, автоматическое предложение
// не перезаписывает его. Хендлер отдаёт 409 ZUN_ALREADY_SET.
var ErrZunAlreadySet = errors.New("ZUN_ALREADY_SET")

// zunApplyRequest — тело POST /runs/{id}/zun/suggestions/apply.
type zunApplyRequest struct {
	NodeID    string   `json:"node_id"`
	Knowledge []string `json:"knowledge"`
	Skills    []string `json:"skills"`
	Abilities []string `json:"abilities"`
}

// zunAppliedNode — результат успешной записи: узел, которому сохранён ЗУН.
type zunAppliedNode struct {
	NodeID    string
	NodeTitle string
	Zun       domain.Zun
}

// zunSuggestionWriter — optional-возможность хранилища: атомарно сохранить
// предложенный ЗУН, только если текущий пуст. Её реализуют *PGStore (row lock
// в транзакции) и *memStore; для остальных хранилищ POST отвечает 501.
type zunSuggestionWriter interface {
	applyZunSuggestions(ctx context.Context, runID, nodeID string, zun domain.Zun) (zunAppliedNode, error)
}

// zunApplyResponse — публичный DTO успешного применения предложения. Это
// обновлённое предложение: can_apply=false, current_zun — сохранённое значение.
type zunApplyResponse struct {
	RunID      string      `json:"run_id"`
	NodeID     string      `json:"node_id"`
	NodeTitle  string      `json:"node_title"`
	Source     string      `json:"source"`
	Applied    bool        `json:"applied"`
	CanApply   bool        `json:"can_apply"`
	CurrentZun *domain.Zun `json:"current_zun"`
}

// normalizeZunSuggestionList нормализует один список ЗУН: trim, схлопывание
// внутренних пробелов, отбрасывание пустых, дедупликация без учёта регистра,
// ограничения на длину пункта и количество пунктов. Результат всегда не nil.
func normalizeZunSuggestionList(items []string) ([]string, error) {
	out := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		value := strings.Join(strings.Fields(item), " ")
		if value == "" {
			continue
		}
		if len([]rune(value)) > zunSuggestionMaxItemRunes {
			return nil, fmt.Errorf("пункт ЗУН длиннее %d символов", zunSuggestionMaxItemRunes)
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
		if len(out) > zunSuggestionMaxItemsPerList {
			return nil, fmt.Errorf("в списке ЗУН больше %d пунктов", zunSuggestionMaxItemsPerList)
		}
	}
	return out, nil
}

// normalizeZunSuggestionInput валидирует и нормализует тело apply-запроса.
// Возвращает 400-совместимую ошибку для пустого node_id или пустого ЗУН.
func normalizeZunSuggestionInput(req zunApplyRequest) (string, domain.Zun, error) {
	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" {
		return "", domain.Zun{}, fmt.Errorf("node_id обязателен")
	}
	knowledge, err := normalizeZunSuggestionList(req.Knowledge)
	if err != nil {
		return "", domain.Zun{}, err
	}
	skills, err := normalizeZunSuggestionList(req.Skills)
	if err != nil {
		return "", domain.Zun{}, err
	}
	abilities, err := normalizeZunSuggestionList(req.Abilities)
	if err != nil {
		return "", domain.Zun{}, err
	}
	if len(knowledge)+len(skills)+len(abilities) == 0 {
		return "", domain.Zun{}, fmt.Errorf("нужен хотя бы один пункт ЗУН")
	}
	return nodeID, domain.Zun{Knowledge: knowledge, Skills: skills, Abilities: abilities}, nil
}

// applyZunSuggestions сохраняет предложенный ЗУН в PostgreSQL.
//
// Узел читается с FOR UPDATE в транзакции: проверка «ЗУН пуст» и запись
// происходят под блокировкой строки, поэтому два параллельных apply не могут
// перезаписать друг друга. Любой непустой ЗУН (ручной или от enrichZun)
// защищён: возвращается ErrZunAlreadySet без записи.
func (p *PGStore) applyZunSuggestions(ctx context.Context, runID, nodeID string, zun domain.Zun) (zunAppliedNode, error) {
	var applied zunAppliedNode
	if p == nil || p.Pool == nil {
		return applied, ErrServiceUnavailable
	}
	if !validUUID(runID) || !validUUID(nodeID) {
		return applied, ErrNotFound
	}
	payload, err := json.Marshal(zun)
	if err != nil {
		return applied, fmt.Errorf("marshal zun: %w", err)
	}

	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return applied, err
	}
	defer tx.Rollback(context.Background())

	var title string
	var currentRaw []byte
	err = tx.QueryRow(ctx, `SELECT title, COALESCE(zun,'null'::jsonb) FROM nodes WHERE id=$1 AND run_id=$2 FOR UPDATE`,
		nodeID, runID).Scan(&title, &currentRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return applied, ErrNotFound
	}
	if err != nil {
		return applied, translate(err)
	}
	var current domain.Zun
	if len(currentRaw) > 0 && string(currentRaw) != "null" {
		if decodeErr := json.Unmarshal(currentRaw, &current); decodeErr != nil {
			return applied, fmt.Errorf("decode node zun: %w", decodeErr)
		}
	}
	if !zunIsEmpty(&current) {
		return applied, ErrZunAlreadySet
	}
	if _, err = tx.Exec(ctx, `UPDATE nodes SET zun=$3, updated_at=now() WHERE id=$1 AND run_id=$2`,
		nodeID, runID, payload); err != nil {
		return applied, translate(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return applied, translate(err)
	}
	return zunAppliedNode{NodeID: nodeID, NodeTitle: title, Zun: zun}, nil
}

// applyZunSuggestions — in-memory реализация для demo/unit-тестов.
func (m *memStore) applyZunSuggestions(_ context.Context, runID, nodeID string, zun domain.Zun) (zunAppliedNode, error) {
	var applied zunAppliedNode
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.runs[runID]; !ok {
		return applied, ErrNotFound
	}
	nodes := m.nodes[runID]
	for i := range nodes {
		if nodes[i].ID != nodeID {
			continue
		}
		if !zunIsEmpty(nodes[i].Zun) {
			return applied, ErrZunAlreadySet
		}
		copied := zun
		nodes[i].Zun = &copied
		return zunAppliedNode{NodeID: nodeID, NodeTitle: nodes[i].Title, Zun: zun}, nil
	}
	return applied, ErrNotFound
}

// handleZunSuggestions godoc
// @Summary      Rule-based ZUN suggestions for a run tree
// @Description  Returns deterministic, rule-based knowledge/skills/abilities suggestions for every node of the run tree. Nodes with a non-empty ZUN keep it untouched: their suggestion lists are empty and can_apply=false. source is always "rule" (no LLM). Expert roles only (admin/teacher/rop).
// @Tags         runs
// @Produce      json
// @Param        id   path      string  true  "Run ID"
// @Success      200  {array}   zunSuggestion
// @Failure      400  {object}  errorBody
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/zun/suggestions [get]
func (s *Server) handleZunSuggestions(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP) {
		return
	}
	runID := strings.TrimSpace(r.PathValue("id"))
	if runID == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "не указан идентификатор исследования")
		return
	}
	run, err := s.store.GetRun(runID)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "исследование не найдено")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось загрузить исследование")
		return
	}
	nodes, err := s.store.GetTree(runID)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "исследование не найдено")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось загрузить дерево исследования")
		return
	}
	suggestions := make([]zunSuggestion, 0, len(nodes))
	for _, node := range nodes {
		suggestions = append(suggestions, buildZunSuggestion(run.Profession, node))
	}
	writeJSON(w, http.StatusOK, suggestions)
}

// handleApplyZunSuggestion godoc
// @Summary      Apply a rule-based ZUN suggestion to one node
// @Description  Persists the supplied knowledge/skills/abilities to one node of the run tree, but only when the node has no ZUN yet. Existing manual or auto-generated ZUN is never overwritten: the request fails with 409 ZUN_ALREADY_SET. Node must belong to the run (404 otherwise). Expert roles only (admin/teacher/rop).
// @Tags         runs
// @Accept       json
// @Produce      json
// @Param        id    path      string          true  "Run ID"
// @Param        body  body      zunApplyRequest true  "Node id and ZUN lists"
// @Success      200   {object}  zunApplyResponse
// @Failure      400   {object}  errorBody
// @Failure      401   {object}  errorBody
// @Failure      403   {object}  errorBody
// @Failure      404   {object}  errorBody
// @Failure      409   {object}  errorBody
// @Failure      500   {object}  errorBody
// @Failure      501   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/zun/suggestions/apply [post]
func (s *Server) handleApplyZunSuggestion(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP) {
		return
	}
	runID := strings.TrimSpace(r.PathValue("id"))
	if runID == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "не указан идентификатор исследования")
		return
	}

	var req zunApplyRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "некорректный JSON")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "после тела запроса есть лишние данные")
		return
	}
	nodeID, zun, err := normalizeZunSuggestionInput(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	writer, ok := s.store.(zunSuggestionWriter)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "для сохранения ЗУН требуется PostgreSQL")
		return
	}
	applied, err := writer.applyZunSuggestions(r.Context(), runID, nodeID, zun)
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "исследование или узел не найдены")
		return
	case errors.Is(err, ErrZunAlreadySet):
		writeErr(w, http.StatusConflict, "ZUN_ALREADY_SET", "у узла уже есть ЗУН: автоматическое предложение не перезаписывает существующие значения")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось сохранить ЗУН")
		return
	}
	writeJSON(w, http.StatusOK, zunApplyResponse{
		RunID:      runID,
		NodeID:     applied.NodeID,
		NodeTitle:  applied.NodeTitle,
		Source:     zunSuggestionSourceRule,
		Applied:    true,
		CanApply:   false,
		CurrentZun: &applied.Zun,
	})
}
