package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/logging"
)

// retryChat is the optional chat capability behind POST /runs/{id}/retry:
// rebuild the transcript from the durable dialog and repeat the post-interview
// pipeline. Services without it (the demo/in-memory setup) answer 501.
type retryChat interface {
	// CheckRetryFinalize validates the retry preconditions without changing any
	// state, so the HTTP layer can still answer 404/409/501 honestly.
	CheckRetryFinalize(ctx context.Context, runID string) error
	// RetryFinalize runs the repeatable finalization.
	RetryFinalize(ctx context.Context, runID string) error
}

// The production chat service must stay compatible with the HTTP layer.
var _ retryChat = (*chat.Service)(nil)

// retryRunResult is the 202 body of POST /runs/{id}/retry. Status describes the
// background retry, not the run row: the pipeline finishes later and the run
// status becomes done, degraded or failed again.
type retryRunResult struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

// retryLocks is the in-process guard against two retries of the same run. It is
// intentionally scoped to one Server instance: it protects this backend, not a
// cluster (two instances still need a database-level guard).
type retryLocks struct {
	mu     sync.Mutex
	active map[string]struct{}
}

// begin claims the retry slot of a run and reports whether it was free.
func (l *retryLocks) begin(runID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active == nil {
		l.active = make(map[string]struct{})
	}
	if _, busy := l.active[runID]; busy {
		return false
	}
	l.active[runID] = struct{}{}
	return true
}

// end releases the retry slot of a run.
func (l *retryLocks) end(runID string) {
	l.mu.Lock()
	delete(l.active, runID)
	l.mu.Unlock()
}

// isActive reports whether a retry of the run is running in this process.
func (l *retryLocks) isActive(runID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, busy := l.active[runID]
	return busy
}

// handleRetryRun repeats the post-interview pipeline of a run whose interview
// is over but whose tree was never built (for example a failed analysis).
//
// The endpoint validates the retry synchronously (404 unknown run,
// 409 tree already exists / nothing to retry / already running, 501 no
// pipeline) and answers 202 as soon as the background pipeline is started. An
// unavailable LLM is not rejected here: the retry runs, fails and leaves the
// run failed again, so the client may retry once the provider is back.
func (s *Server) handleRetryRun(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser, auth.RoleSpecialist) {
		return
	}
	chatService, ok := s.chat.(retryChat)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "повторная финализация недоступна: чат-сервис не настроен")
		return
	}
	runID := r.PathValue("id")
	if !s.allowInterviewAccess(w, r, runID) {
		return
	}
	if !s.retryLocks.begin(runID) {
		writeErr(w, http.StatusConflict, "RETRY_IN_PROGRESS", "повторный анализ уже выполняется")
		return
	}
	// A deterministic emergency tree is replaceable: remove it before the real
	// retry, otherwise CheckRetryFinalize would reject the run forever.
	if pgStore, ok := s.store.(*PGStore); ok {
		if reason, reasonErr := pgStore.DegradedReason(runID); reasonErr == nil &&
			strings.Contains(strings.ToLower(reason), "демонстрационный") {
			if clearErr := pgStore.ClearRunTree(runID); clearErr != nil {
				s.retryLocks.end(runID)
				logging.Logger.Error("retry_clear_fallback_failed", "run_id", runID, "error", clearErr)
				writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось подготовить повторный анализ")
				return
			}
		}
	}
	// Check before the 202: a retry that can never start must be a real error,
	// not a background task that fails silently.
	if err := chatService.CheckRetryFinalize(r.Context(), runID); err != nil {
		s.retryLocks.end(runID)
		writeRetryRunError(w, err)
		return
	}
	// The pipeline may take minutes; the request context is not used, so a
	// client disconnect does not abort the analysis half-way.
	go func() {
		defer s.retryLocks.end(runID)
		if err := chatService.RetryFinalize(context.Background(), runID); err != nil {
			logging.Logger.Warn("run_retry_failed", "run_id", runID, "error", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, retryRunResult{RunID: runID, Status: string(domain.RunRunning)})
}

// writeRetryRunError maps the retry preconditions to their stable HTTP codes.
func writeRetryRunError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, chat.ErrTreeAlreadyExists):
		writeErr(w, http.StatusConflict, "TREE_ALREADY_EXISTS", "дерево уже построено: повторный анализ не требуется")
	case errors.Is(err, chat.ErrNothingToRetry):
		writeErr(w, http.StatusConflict, "NOTHING_TO_RETRY", "в прогоне нет сохранённых сообщений интервью")
	case errors.Is(err, chat.ErrRetryInProgress):
		writeErr(w, http.StatusConflict, "RETRY_IN_PROGRESS", "интервью или повторный анализ прогона ещё выполняется")
	case errors.Is(err, chat.ErrVersionConflict):
		writeErr(w, http.StatusConflict, "CONFLICT", "состояние интервью изменилось; повторите запрос")
	case errors.Is(err, chat.ErrFinalizerUnavailable):
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "повторная финализация недоступна: не настроен LLM-аналитик")
	case errors.Is(err, chat.ErrNotFound):
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "прогон не найден")
	case errors.Is(err, context.DeadlineExceeded):
		writeErr(w, http.StatusGatewayTimeout, "GATEWAY_TIMEOUT", "не удалось проверить состояние прогона вовремя")
	default:
		logging.Logger.Error("run_retry_internal_error", "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось запустить повторный анализ")
	}
}
