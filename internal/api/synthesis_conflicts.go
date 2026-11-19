package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/store"
	"activity-descriptor/internal/store/db"

	"github.com/jackc/pgx/v5/pgtype"
)

// Разрешение конфликтов синтезатора и память агента принадлежат детерминированному
// merge, поэтому после синтеза они сохраняются в agent_messages с отдельными
// типами сообщений — таблица уже существует с первой миграции и не требует
// изменения схемы. Строки не являются исходящими событиями: outbox-воркер
// выбирает только message_type='run.completed', а статус 'sent' помечает запись
// как терминальную, чтобы она не попала в очередь доставки.
const (
	synthesisConflictMessageType = "synthesis.conflict"
	synthesisMessageMessageType  = "synthesis.message"

	// dedupKey-префиксы задают стабильный порядок выдачи и защищают от повторной
	// записи одних и тех же элементов при повторном запуске финализатора.
	synthesisConflictDedupPrefix = "synthesis.conflict:"
	synthesisMessageDedupPrefix  = "synthesis.message:"
)

// synthesisReviewReader — узкий читающий интерфейс review-эндпоинтов.
// Его реализует *PGStore; unit-тесты подменяют заглушкой.
type synthesisReviewReader interface {
	listSynthesisConflicts(ctx context.Context, runID string) ([]domain.Conflict, error)
	listSynthesisMessages(ctx context.Context, runID string) ([]agent.AgentMessage, error)
}

// SaveSynthesisReview сохраняет конфликты и сообщения памяти, порождённые
// детерминированным синтезатором, чтобы эксперт мог их проверить.
//
// Запись идёт в существующую agent_messages (run_id + payload JSONB), без
// миграции: отдельного хранилища конфликтов в схеме нет. Раньше сохранённые
// элементы того же run заменяются в одной транзакции, поэтому повторный вызов
// идемпотентен и не оставляет устаревших строк.
func (p *PGStore) SaveSynthesisReview(ctx context.Context, runID string, conflicts []domain.Conflict, messages []agent.AgentMessage) error {
	if p == nil || p.Pool == nil {
		return ErrServiceUnavailable
	}
	if !validUUID(runID) {
		return ErrNotFound
	}
	var runUUID pgtype.UUID
	if err := runUUID.Scan(runID); err != nil || !runUUID.Valid {
		return ErrNotFound
	}

	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin synthesis review: %w", err)
	}
	defer tx.Rollback(context.Background())

	var runExists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM runs WHERE id=$1)`, runUUID).Scan(&runExists); err != nil {
		return translate(err)
	}
	if !runExists {
		return ErrNotFound
	}

	if _, err = tx.Exec(ctx, `DELETE FROM agent_messages WHERE run_id=$1 AND message_type IN ($2,$3)`,
		runUUID, synthesisConflictMessageType, synthesisMessageMessageType); err != nil {
		return fmt.Errorf("clear synthesis review: %w", err)
	}

	for index, conflict := range conflicts {
		payload, marshalErr := json.Marshal(conflict)
		if marshalErr != nil {
			return fmt.Errorf("marshal conflict %q: %w", conflict.ID, marshalErr)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO agent_messages(run_id,message_type,payload,status,sent_at,dedup_key)
VALUES($1,$2,$3,$4,now(),$5)`,
			runUUID, synthesisConflictMessageType, string(payload), "sent",
			fmt.Sprintf("%s%06d", synthesisConflictDedupPrefix, index)); err != nil {
			return fmt.Errorf("insert conflict %q: %w", conflict.ID, err)
		}
	}
	for index, message := range messages {
		payload, marshalErr := json.Marshal(message)
		if marshalErr != nil {
			return fmt.Errorf("marshal agent message %q: %w", message.ID, marshalErr)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO agent_messages(run_id,message_type,payload,status,sent_at,dedup_key)
VALUES($1,$2,$3,$4,now(),$5)`,
			runUUID, synthesisMessageMessageType, string(payload), "sent",
			fmt.Sprintf("%s%06d", synthesisMessageDedupPrefix, index)); err != nil {
			return fmt.Errorf("insert agent message %q: %w", message.ID, err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit synthesis review: %w", err)
	}
	return nil
}

// listSynthesisConflicts возвращает сохранённые конфликты run в порядке синтеза.
func (p *PGStore) listSynthesisConflicts(ctx context.Context, runID string) ([]domain.Conflict, error) {
	raw, err := p.listSynthesisReviewPayloads(ctx, runID, synthesisConflictMessageType)
	if err != nil {
		return nil, err
	}
	items := make([]domain.Conflict, 0, len(raw))
	for _, payload := range raw {
		var item domain.Conflict
		if unmarshalErr := json.Unmarshal(payload, &item); unmarshalErr != nil {
			return nil, fmt.Errorf("decode conflict: %w", unmarshalErr)
		}
		items = append(items, item)
	}
	return items, nil
}

// listSynthesisMessages возвращает сохранённую память агента run в порядке синтеза.
func (p *PGStore) listSynthesisMessages(ctx context.Context, runID string) ([]agent.AgentMessage, error) {
	raw, err := p.listSynthesisReviewPayloads(ctx, runID, synthesisMessageMessageType)
	if err != nil {
		return nil, err
	}
	items := make([]agent.AgentMessage, 0, len(raw))
	for _, payload := range raw {
		var item agent.AgentMessage
		if unmarshalErr := json.Unmarshal(payload, &item); unmarshalErr != nil {
			return nil, fmt.Errorf("decode agent message: %w", unmarshalErr)
		}
		items = append(items, item)
	}
	return items, nil
}

func (p *PGStore) listSynthesisReviewPayloads(ctx context.Context, runID, messageType string) ([][]byte, error) {
	if p == nil || p.Pool == nil {
		return nil, ErrServiceUnavailable
	}
	if _, err := p.GetRun(runID); err != nil {
		return nil, err
	}
	var runUUID pgtype.UUID
	if err := runUUID.Scan(runID); err != nil || !runUUID.Valid {
		return nil, ErrNotFound
	}
	rows, err := p.Pool.Query(ctx, `SELECT payload FROM agent_messages
WHERE run_id=$1 AND message_type=$2
ORDER BY dedup_key, created_at, id`, runUUID, messageType)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	items := [][]byte{}
	for rows.Next() {
		var payload []byte
		if err = rows.Scan(&payload); err != nil {
			return nil, err
		}
		items = append(items, payload)
	}
	return items, rows.Err()
}

// ListRunSources возвращает метаданные источников, привязанных к исследованию
// через run_sources. Полный content не читается: детерминированному синтезатору
// для приоритетов и разрешения конфликтов нужны только id, тип и заголовок.
func (p *PGStore) ListRunSources(ctx context.Context, runID string) ([]domain.Source, error) {
	if p == nil || p.Pool == nil {
		return nil, ErrServiceUnavailable
	}
	if !validUUID(runID) {
		return nil, ErrNotFound
	}
	var runUUID pgtype.UUID
	if err := runUUID.Scan(runID); err != nil || !runUUID.Valid {
		return nil, ErrNotFound
	}
	items, err := store.New(p.Pool).ListSourcesByRun(ctx, runUUID)
	if err != nil {
		return nil, translate(err)
	}
	out := make([]domain.Source, 0, len(items))
	for _, item := range items {
		sourceType := domain.SourceType(item.Type)
		out = append(out, domain.Source{
			ID:        item.ID.String(),
			Type:      sourceType,
			Title:     item.Title,
			SourceURL: item.SourceUrl.String,
			Priority:  domain.SourcePriority(sourceType),
		})
	}
	return out, nil
}

// CreateInterviewSource сохраняет стенограмму интервью как источник run.
// Идентификатор источника генерирует вызывающая сторона, чтобы подтверждения
// дерева и этот источник использовали один и тот же UUID.
func (p *PGStore) CreateInterviewSource(ctx context.Context, runID, sourceID, transcript string) error {
	if p == nil || p.Pool == nil {
		return ErrServiceUnavailable
	}
	var runUUID, sourceUUID pgtype.UUID
	if err := runUUID.Scan(runID); err != nil || !runUUID.Valid {
		return ErrNotFound
	}
	if err := sourceUUID.Scan(sourceID); err != nil || !sourceUUID.Valid {
		return &domain.TreeValidationError{Code: "INVALID_REFERENCE", Details: "ожидается UUID источника"}
	}
	now := time.Now().UTC()
	_, err := store.New(p.Pool).CreateSource(ctx, db.CreateSourceParams{
		ID:               sourceUUID,
		RunID:            runUUID,
		Type:             string(domain.SourceInterview),
		Title:            "Interview transcript",
		Content:          pgtype.Text{String: transcript, Valid: true},
		RawPayload:       nil,
		ProcessingStatus: "processed",
		ProcessedAt:      pgtype.Timestamptz{Time: now, Valid: true},
		Provider:         pgtype.Text{String: "ai", Valid: true},
	})
	if err != nil {
		return translate(err)
	}
	return nil
}

// SaveRunSnapshot сохраняет автоснимок текущего дерева run.
func (p *PGStore) SaveRunSnapshot(ctx context.Context, runID string) error {
	if p == nil || p.Pool == nil {
		return ErrServiceUnavailable
	}
	if !validUUID(runID) {
		return ErrNotFound
	}
	var runUUID pgtype.UUID
	if err := runUUID.Scan(runID); err != nil || !runUUID.Valid {
		return ErrNotFound
	}
	if _, err := store.New(p.Pool).SaveSnapshot(ctx, runUUID); err != nil {
		return translate(err)
	}
	return nil
}

// synthesisReviewError переводит ошибки хранилища в HTTP-коды review-эндпоинтов.
func synthesisReviewError(w http.ResponseWriter, err error) {
	if mapStoreError(w, err) {
		return
	}
	writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось получить данные синтеза")
}

// handleRunConflicts godoc
// @Summary      Resolved synthesizer conflicts for a run
// @Description  Returns title/description conflicts detected and deterministically resolved by the synthesizer during finalization. Conflict values keep both competing sources and the winner (interview > standard > vacancy > document). Empty array when the run has no stored conflicts.
// @Tags         runs
// @Produce      json
// @Param        id   path      string  true  "Run ID"
// @Success      200  {array}   domain.Conflict
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/conflicts [get]
func (s *Server) handleRunConflicts(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleStudent) {
		return
	}
	reader, ok := s.store.(synthesisReviewReader)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "для конфликтов синтеза требуется PostgreSQL")
		return
	}
	items, err := reader.listSynthesisConflicts(r.Context(), r.PathValue("id"))
	if err != nil {
		synthesisReviewError(w, err)
		return
	}
	if items == nil {
		items = []domain.Conflict{}
	}
	writeJSON(w, http.StatusOK, items)
}

// handleRunAgentMessages godoc
// @Summary      Synthesizer agent memory messages for a run
// @Description  Returns the agent memory messages emitted by the deterministic synthesizer during finalization. Messages of kind "conflict" carry the resolved domain.Conflict in payload. Empty array when the run has no stored messages.
// @Tags         runs
// @Produce      json
// @Param        id   path      string  true  "Run ID"
// @Success      200  {array}   agent.AgentMessage
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/agent-messages [get]
func (s *Server) handleRunAgentMessages(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP) {
		return
	}
	reader, ok := s.store.(synthesisReviewReader)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "для сообщений синтеза требуется PostgreSQL")
		return
	}
	items, err := reader.listSynthesisMessages(r.Context(), r.PathValue("id"))
	if err != nil {
		synthesisReviewError(w, err)
		return
	}
	if items == nil {
		items = []agent.AgentMessage{}
	}
	writeJSON(w, http.StatusOK, items)
}
