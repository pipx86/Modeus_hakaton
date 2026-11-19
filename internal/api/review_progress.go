package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
)

// Прогресс проверки подтверждений (mentor feature #7 "Review Progress").
//
// Контракт:
//
//	GET /api/v1/reviews/evidence/progress[?run_id=<uuid>]
//
// Endpoint отдаёт счётчики для рабочего места эксперта: сколько подтверждений
// ждут проверки, сколько подтверждено и сколько отклонено. Без run_id счётчики
// глобальные (по всем исследованиям); с run_id — только по этому прогону.
// Фронтенд использует ответ для строки «подтверждено X из Y» и для бейджей
// клавиатурного режима проверки, поэтому порядок полей и значения детерминированы.
//
// Ответ:
//
//	{
//	  "scope": "global" | "run",
//	  "run_id": "…",          // только при scope=run
//	  "pending": 7,
//	  "approved": 12,
//	  "rejected": 3,
//	  "total": 22
//	}
//
// total == pending + approved + rejected. Статус берётся из evidence.status
// (миграция 20261118000000_evidence_status.sql); для записей без статуса
// используется "pending", как и в очереди проверки.
//
// Параметр run_id обязан быть UUID: синтаксически неверный -> 400, корректный,
// но несуществующий -> 404 (иначе экспертный интерфейс не отличит опечатку от
// пустого прогона).
//
// Права: admin, teacher, rop — как у очереди проверки. student и guest
// получают 403. Коды: 200 — счётчики; 400 — невалидный run_id; 401 — нет
// токена; 403 — роль не экспертная; 404 — run_id не найден; 500 — ошибка БД;
// 501 — хранилище не поддерживает счётчики (in-memory хранилище их умеет).
//
// Это отдельный endpoint, а не параметр очереди: hotkeys/прогрессу нужен
// лёгкий ответ без пагинации и без выдачи самих цитат.

const (
	reviewProgressScopeGlobal = "global"
	reviewProgressScopeRun    = "run"
)

// reviewProgressCounts — счётчики по статусам, вычисленные хранилищем.
// Total всегда равен сумме трёх статусов.
type reviewProgressCounts struct {
	Pending  int
	Approved int
	Rejected int
	Total    int
}

// reviewProgressResponse — публичный DTO endpoint'а. Scope объясняет, как
// читать счётчики: глобально или в рамках одного прогона.
type reviewProgressResponse struct {
	Scope    string `json:"scope"`
	RunID    string `json:"run_id,omitempty"`
	Pending  int    `json:"pending"`
	Approved int    `json:"approved"`
	Rejected int    `json:"rejected"`
	Total    int    `json:"total"`
}

// reviewProgressReader — узкий интерфейс хендлера. Его реализуют *PGStore
// (SQL) и *memStore (память); unit-тесты подменяют его заглушкой.
type reviewProgressReader interface {
	evidenceReviewProgress(ctx context.Context, runID string) (reviewProgressCounts, error)
}

// parseReviewProgressQuery валидирует run_id. Пустая строка означает
// глобальные счётчики. Ошибка предназначена для 400 BAD_REQUEST.
func parseReviewProgressQuery(values url.Values) (string, error) {
	runID := strings.TrimSpace(values.Get("run_id"))
	if runID == "" {
		return "", nil
	}
	if !validUUID(runID) {
		return "", fmt.Errorf("run_id должен быть UUID")
	}
	return runID, nil
}

// evidenceReviewProgress считает подтверждения по статусам в PostgreSQL.
// Глобальная область — вся таблица evidence; область прогона — строки
// evidence.run_id (колонка заполняется триггером с миграции
// 20261109000003_evidence_scope.sql и не требует JOIN с nodes).
//
// Существование run проверяется отдельно: агрегатный запрос для неизвестного
// UUID вернул бы нули, а контракт требует 404.
func (p *PGStore) evidenceReviewProgress(ctx context.Context, runID string) (reviewProgressCounts, error) {
	counts := reviewProgressCounts{}
	if p == nil || p.Pool == nil {
		return counts, ErrServiceUnavailable
	}

	var query string
	var args []any
	if runID == "" {
		query = `SELECT
 count(*) FILTER (WHERE COALESCE(status,'pending')='pending'),
 count(*) FILTER (WHERE COALESCE(status,'pending')='approved'),
 count(*) FILTER (WHERE COALESCE(status,'pending')='rejected')
FROM evidence`
	} else {
		var runUUID = runID
		var exists bool
		if err := p.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM runs WHERE id=$1)`, runUUID).Scan(&exists); err != nil {
			return counts, translate(err)
		}
		if !exists {
			return counts, ErrNotFound
		}
		query = `SELECT
 count(*) FILTER (WHERE COALESCE(status,'pending')='pending'),
 count(*) FILTER (WHERE COALESCE(status,'pending')='approved'),
 count(*) FILTER (WHERE COALESCE(status,'pending')='rejected')
FROM evidence WHERE run_id=$1`
		args = append(args, runUUID)
	}

	if err := p.Pool.QueryRow(ctx, query, args...).Scan(&counts.Pending, &counts.Approved, &counts.Rejected); err != nil {
		return counts, translate(err)
	}
	counts.Total = counts.Pending + counts.Approved + counts.Rejected
	return counts, nil
}

// evidenceReviewProgress — in-memory реализация для demo/unit-тестов.
// Статус подтверждения хранится в трёх местах memStore (status map, approved
// map и Evidence.Status), поэтому читаем их в том же приоритете, в котором их
// обновляют ApproveEvidence/RejectEvidence/BulkApproveEvidence/SaveTree.
func (m *memStore) evidenceReviewProgress(_ context.Context, runID string) (reviewProgressCounts, error) {
	counts := reviewProgressCounts{}
	m.mu.RLock()
	defer m.mu.RUnlock()

	if runID != "" {
		if _, ok := m.runs[runID]; !ok {
			return counts, ErrNotFound
		}
		for _, id := range m.runEvidenceIDs[runID] {
			evidence, ok := m.evidences[id]
			if !ok {
				continue
			}
			counts.add(m.evidenceStatusLocked(id, evidence))
		}
		counts.Total = counts.Pending + counts.Approved + counts.Rejected
		return counts, nil
	}
	for id, evidence := range m.evidences {
		counts.add(m.evidenceStatusLocked(id, evidence))
	}
	counts.Total = counts.Pending + counts.Approved + counts.Rejected
	return counts, nil
}

// evidenceStatusLocked вычисляет эффективный статус подтверждения в memStore.
// Вызывается с удержанным RLock.
func (m *memStore) evidenceStatusLocked(id string, evidence domain.Evidence) domain.EvidenceStatus {
	if status := m.status[id]; status != "" {
		return status
	}
	if m.approved[id] {
		return domain.EvidenceApproved
	}
	switch evidence.Status {
	case domain.EvidenceApproved, domain.EvidenceRejected:
		return evidence.Status
	default:
		return domain.EvidencePending
	}
}

// add учитывает один статус в счётчиках. Неизвестные значения трактуются как
// pending: это совпадает с COALESCE(status,'pending') в SQL и не теряет записи
// из total.
func (c *reviewProgressCounts) add(status domain.EvidenceStatus) {
	switch status {
	case domain.EvidenceApproved:
		c.Approved++
	case domain.EvidenceRejected:
		c.Rejected++
	default:
		c.Pending++
	}
}

// handleReviewProgress godoc
// @Summary      Evidence review progress counters
// @Description  Returns how many evidence rows are pending, approved and rejected. Without run_id the counters are global; with run_id they are scoped to that run (malformed run_id -> 400, unknown run -> 404). total always equals pending+approved+rejected. Expert roles only (admin/teacher/rop); used by the review progress counter and keyboard review mode.
// @Tags         evidences
// @Produce      json
// @Param        run_id  query  string  false  "Optional run UUID filter"
// @Success      200  {object}  reviewProgressResponse
// @Failure      400  {object}  errorBody
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/reviews/evidence/progress [get]
func (s *Server) handleReviewProgress(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleSpecialist) {
		return
	}
	runID, err := parseReviewProgressQuery(r.URL.Query())
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	reader, ok := s.store.(reviewProgressReader)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "для прогресса проверки требуется хранилище со счётчиками")
		return
	}
	counts, err := reader.evidenceReviewProgress(r.Context(), runID)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "исследование не найдено")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось посчитать прогресс проверки")
		return
	}
	response := reviewProgressResponse{
		Scope:    reviewProgressScopeGlobal,
		Pending:  counts.Pending,
		Approved: counts.Approved,
		Rejected: counts.Rejected,
		Total:    counts.Total,
	}
	if runID != "" {
		response.Scope = reviewProgressScopeRun
		response.RunID = runID
	}
	writeJSON(w, http.StatusOK, response)
}
