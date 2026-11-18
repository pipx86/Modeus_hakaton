package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
)

// Очередь спорных подтверждений (disputed evidence review queue).
//
// Контракт:
//
//	GET /api/v1/reviews/evidence?status=pending|rejected&limit=N&offset=N
//
// Назначение — рабочее место эксперта: одним запросом получить подтверждения
// (цитаты), которые ещё не проверены или были отклонены, вместе с контекстом
// для решения (узел дерева, источник, run).
//
// Параметры:
//   - status — "pending" (по умолчанию) или "rejected". Очередь намеренно не
//     отдаёт approved: это не спорные записи. Любое другое значение — 400.
//   - limit  — размер страницы, 1..200, по умолчанию 50. Вне диапазона — 400.
//   - offset — смещение, >= 0, по умолчанию 0. Отрицательное/не число — 400.
//
// Ответ — JSON-массив строк очереди в стабильном порядке (created_at, id),
// поэтому пагинация limit/offset детерминирована:
//
//	[{
//	  "id": "…",            // evidence.id
//	  "run_id": "…",        // run, к которому относится подтверждение
//	  "node_id": "…",       // узел дерева
//	  "node_title": "…",
//	  "source_id": "…",
//	  "source_title": "…",
//	  "source_url": "…",
//	  "ref_id": "…",        // якорь фрагмента в источнике
//	  "quote": "…",
//	  "status": "pending"
//	}]
//
// Пустая страница — `[]` (не `null`).
//
// Права: только экспертные роли admin, teacher, rop. Роли student и guest
// получают 403: это не публичный просмотр, а рабочая очередь проверки.
// Коды: 200 — страница очереди; 400 — параметры невалидны; 401 — нет токена;
// 403 — роль не экспертная; 500 — ошибка БД; 501 — хранилище не PostgreSQL.
//
// Отдельный endpoint выбран вместо расширения GET /api/v1/evidences, чтобы не
// менять уже используемый фронтендом контракт списка подтверждений.

const (
	reviewQueueDefaultLimit = 50
	reviewQueueMaxLimit     = 200
)

// reviewQueueReader — узкий интерфейс хендлера очереди. Его реализует
// *PGStore; unit-тесты подменяют его заглушкой.
type reviewQueueReader interface {
	listReviewQueue(ctx context.Context, status domain.EvidenceStatus, limit, offset int) ([]disputedEvidenceItem, error)
}

// disputedEvidenceItem — одна строка очереди проверки. SourceTitle/SourceURL
// нужны, чтобы эксперт не делал отдельный запрос за источником.
type disputedEvidenceItem struct {
	ID          string                `json:"id"`
	RunID       string                `json:"run_id"`
	NodeID      string                `json:"node_id"`
	NodeTitle   string                `json:"node_title,omitempty"`
	SourceID    string                `json:"source_id"`
	SourceTitle string                `json:"source_title,omitempty"`
	SourceURL   string                `json:"source_url,omitempty"`
	RefID       string                `json:"ref_id,omitempty"`
	Quote       string                `json:"quote"`
	Status      domain.EvidenceStatus `json:"status"`
}

// parseReviewQueueQuery валидирует query-параметры очереди.
// Возвращает status, limit, offset либо ошибку для 400 с человекочитаемым
// сообщением (контракт ошибок как у остальных хендлеров: code BAD_REQUEST).
func parseReviewQueueQuery(values url.Values) (domain.EvidenceStatus, int, int, error) {
	status := domain.EvidencePending
	if raw := strings.TrimSpace(values.Get("status")); raw != "" {
		candidate := domain.EvidenceStatus(raw)
		if candidate != domain.EvidencePending && candidate != domain.EvidenceRejected {
			return "", 0, 0, fmt.Errorf("status должен быть pending или rejected")
		}
		status = candidate
	}
	limit := reviewQueueDefaultLimit
	if raw := strings.TrimSpace(values.Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > reviewQueueMaxLimit {
			return "", 0, 0, fmt.Errorf("limit должен быть целым числом от 1 до %d", reviewQueueMaxLimit)
		}
		limit = value
	}
	offset := 0
	if raw := strings.TrimSpace(values.Get("offset")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return "", 0, 0, fmt.Errorf("offset должен быть целым неотрицательным числом")
		}
		offset = value
	}
	return status, limit, offset, nil
}

// listReviewQueue — запрос очереди в PostgreSQL. Evidence.status существует с
// миграции 20261118000000_evidence_status.sql, COALESCE нужен на случай
// записей, созданных до неё (там статус = approved/pending по колонке approved).
func (p *PGStore) listReviewQueue(ctx context.Context, status domain.EvidenceStatus, limit, offset int) ([]disputedEvidenceItem, error) {
	rows, err := p.Pool.Query(ctx, `SELECT e.id::text,e.run_id::text,e.node_id::text,COALESCE(n.title,''),
 e.source_id::text,COALESCE(s.title,''),COALESCE(s.source_url,''),COALESCE(e.ref_id,''),e.quote,COALESCE(e.status,'pending')
 FROM evidence e
 JOIN nodes n ON n.id=e.node_id
 JOIN sources s ON s.id=e.source_id
 WHERE COALESCE(e.status,'pending')=$1
 ORDER BY e.created_at,e.id
 LIMIT $2 OFFSET $3`, string(status), limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]disputedEvidenceItem, 0, limit)
	for rows.Next() {
		var item disputedEvidenceItem
		var rawStatus string
		if err = rows.Scan(&item.ID, &item.RunID, &item.NodeID, &item.NodeTitle, &item.SourceID,
			&item.SourceTitle, &item.SourceURL, &item.RefID, &item.Quote, &rawStatus); err != nil {
			return nil, err
		}
		item.Status = domain.EvidenceStatus(rawStatus)
		if item.Status == "" {
			item.Status = domain.EvidencePending
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// handleReviewQueue godoc
// @Summary      Expert review queue for disputed evidence
// @Description  Returns evidence that still needs expert review: status=pending (default) or status=rejected. Each row carries run_id, node_id/node_title, source_id/source_title/source_url, ref_id and the quote, so the reviewer can decide without extra lookups. Paginated with limit (1..200, default 50) and offset (>=0, default 0), stable order by created_at,id; empty page is [].
// @Tags         evidences
// @Produce      json
// @Param        status  query  string  false  "pending or rejected (default pending)"
// @Param        limit   query  int     false  "Page size 1..200 (default 50)"
// @Param        offset  query  int     false  "Offset >= 0 (default 0)"
// @Success      200  {array}   disputedEvidenceItem
// @Failure      400  {object}  errorBody
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/reviews/evidence [get]
func (s *Server) handleReviewQueue(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP) {
		return
	}
	// Параметры проверяем до обращения к хранилищу: невалидный запрос — это
	// 400 при любом бэкенде, в том числе когда очередь не поддерживается.
	status, limit, offset, err := parseReviewQueueQuery(r.URL.Query())
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	reader, ok := s.store.(reviewQueueReader)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "для очереди проверки требуется PostgreSQL")
		return
	}
	items, err := reader.listReviewQueue(r.Context(), status, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось получить очередь проверки")
		return
	}
	if items == nil {
		items = []disputedEvidenceItem{}
	}
	writeJSON(w, http.StatusOK, items)
}
