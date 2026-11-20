package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/logging"
	"activity-descriptor/internal/store"
)

// Interviews of one research run
//
// One run can hold several respondent interviews (see the plan's card 12). The
// run keeps at most one active interview, every interview has its own
// respondent descriptor, and the dialog history is attributed to the interview
// so two respondents are never mixed in one transcript.

// interviewResetter is the optional chat capability that drops the in-memory
// session of a run, so a new interview starts a fresh dialog instead of
// resuming the previous respondent.
type interviewResetter interface {
	ResetSession(runID string)
}

// ListRunInterviews returns every interview of a run, oldest first.
func (p *PGStore) ListRunInterviews(ctx context.Context, runID string) ([]chat.Interview, error) {
	if p == nil || p.Pool == nil {
		return nil, ErrServiceUnavailable
	}
	return store.NewChatSessionStore(p.Pool).ListInterviews(ctx, runID)
}

// GetRunInterview returns one interview of a run.
func (p *PGStore) GetRunInterview(ctx context.Context, runID, interviewID string) (chat.Interview, error) {
	if p == nil || p.Pool == nil {
		return chat.Interview{}, ErrServiceUnavailable
	}
	interviews := store.NewChatSessionStore(p.Pool)
	return interviews.GetInterview(ctx, runID, interviewID)
}

// StartRunInterview closes the active interview, opens a new one for the given
// respondent and resets the dialog cursor.
func (p *PGStore) StartRunInterview(ctx context.Context, runID string, respondent domain.Respondent) (chat.Interview, error) {
	if p == nil || p.Pool == nil {
		return chat.Interview{}, ErrServiceUnavailable
	}
	return store.NewChatSessionStore(p.Pool).StartInterview(ctx, runID, respondent)
}

// FailActiveRunInterview marks the active interview as failed.
func (p *PGStore) FailActiveRunInterview(ctx context.Context, runID string) error {
	if p == nil || p.Pool == nil {
		return ErrServiceUnavailable
	}
	return store.NewChatSessionStore(p.Pool).FailActiveInterview(ctx, runID)
}

// ListInterviewMessages returns the dialog of one interview of a run.
func (p *PGStore) ListInterviewMessages(ctx context.Context, runID, interviewID string) ([]chat.Message, error) {
	if p == nil || p.Pool == nil {
		return nil, ErrServiceUnavailable
	}
	return store.NewChatSessionStore(p.Pool).InterviewMessages(ctx, runID, interviewID)
}

// ensureRunInterview opens the first interview of a chat run, so the initial
// dialog already belongs to a respondent record instead of staying unattributed
// and additional respondents can be added next to it. A non-PostgreSQL store
// (demo, tests) has no interview registry, so it is a no-op there.
func (s *Server) ensureRunInterview(ctx context.Context, runID string) error {
	pg, ok := s.store.(*PGStore)
	if !ok || pg.Pool == nil {
		return nil
	}
	_, err := pg.StartRunInterview(ctx, runID, domain.Respondent{})
	return err
}

// handleListRunInterviews godoc
// @Summary      List interviews of a research run
// @Description  Returns every respondent interview of the run, oldest first. A run keeps at most one active interview; finished interviews stay as history with their own dialog.
// @Tags         interviews
// @Produce      json
// @Param        id   path      string  true  "Run ID"
// @Success      200  {array}   chat.Interview
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/interviews [get]
func (s *Server) handleListRunInterviews(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	if !s.allowInterviewAccess(w, r, runID) {
		return
	}
	pg, ok := requirePG(w, s)
	if !ok {
		return
	}
	interviews, err := pg.ListRunInterviews(r.Context(), runID)
	if err != nil {
		s.writeInterviewError(w, err, "не удалось прочитать интервью исследования")
		return
	}
	if interviews == nil {
		interviews = []chat.Interview{}
	}
	writeJSON(w, http.StatusOK, interviews)
}

// handleStartRunInterview godoc
// @Summary      Start a new respondent interview
// @Description  Closes the active interview of the run, opens a new one for the given respondent and resets the interviewer cursor, so the dialog starts from the first question. Only chat runs that are not finished accept a new interview.
// @Tags         interviews
// @Accept       json
// @Produce      json
// @Param        id    path      string  true  "Run ID"
// @Param        body  body      object{respondent_name=string,respondent_role=string,respondent_ref=string}  false  "Respondent descriptor (anonymised)"
// @Success      201   {object}  chat.Interview
// @Failure      400   {object}  errorBody
// @Failure      401   {object}  errorBody
// @Failure      403   {object}  errorBody
// @Failure      404   {object}  errorBody
// @Failure      409   {object}  errorBody
// @Failure      422   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/interviews [post]
func (s *Server) handleStartRunInterview(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	if !s.allowInterviewAccess(w, r, runID) {
		return
	}
	pg, ok := requirePG(w, s)
	if !ok {
		return
	}

	run, err := s.store.GetRun(runID)
	if err != nil {
		s.writeInterviewError(w, err, "не удалось прочитать исследование")
		return
	}
	if run.Type != "" && run.Type != domain.RunTypeChat {
		writeErr(w, http.StatusConflict, "NOT_A_CHAT_RUN", "новое интервью доступно только для чат-исследования")
		return
	}
	switch run.Status {
	case domain.RunDone, domain.RunFailed:
		writeErr(w, http.StatusConflict, "RUN_FINISHED", "нельзя начать интервью в завершённом исследовании")
		return
	}

	var req struct {
		RespondentName string `json:"respondent_name,omitempty"`
		RespondentRole string `json:"respondent_role,omitempty"`
		RespondentRef  string `json:"respondent_ref,omitempty"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "некорректный JSON")
		return
	}
	respondent := chat.NormalizeRespondent(req.RespondentName, req.RespondentRole, req.RespondentRef)
	if err := chat.ValidateRespondent(respondent); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "INVALID_RESPONDENT", "имя, роль и метка респондента — не длиннее 120 символов")
		return
	}

	interview, err := pg.StartRunInterview(r.Context(), runID, respondent)
	if err != nil {
		s.writeInterviewError(w, err, "не удалось создать интервью")
		return
	}
	// The durable cursor was dropped by the store; the in-memory session must
	// go too, otherwise the interviewer keeps asking the previous respondent's
	// questions.
	if resetter, ok := s.chat.(interviewResetter); ok {
		resetter.ResetSession(runID)
	}
	if s.chat != nil {
		if err := s.chat.Start(runID, run.Profession); err != nil {
			logging.Logger.Error("interview_restart_failed", "run_id", runID, "interview_id", interview.ID, "error", err)
			if failErr := pg.FailActiveRunInterview(r.Context(), runID); failErr != nil {
				logging.Logger.Error("interview_fail_mark_failed", "run_id", runID, "error", failErr)
			}
			writeErr(w, http.StatusBadGateway, "INTERVIEW_START_FAILED", "интервью создано, но диалог не удалось начать")
			return
		}
	}
	writeJSON(w, http.StatusCreated, interview)
}

// handleRunInterviewMessages godoc
// @Summary      Read the dialog of one interview
// @Description  Returns the messages of exactly one respondent interview, so the dialogs of several respondents of the same run never mix.
// @Tags         interviews
// @Produce      json
// @Param        id            path      string  true  "Run ID"
// @Param        interview_id  path      string  true  "Interview ID"
// @Success      200  {array}   chat.Message
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/interviews/{interview_id}/messages [get]
func (s *Server) handleRunInterviewMessages(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	interviewID := r.PathValue("interview_id")
	if !s.allowInterviewAccess(w, r, runID) {
		return
	}
	pg, ok := requirePG(w, s)
	if !ok {
		return
	}
	if _, err := pg.GetRunInterview(r.Context(), runID, interviewID); err != nil {
		s.writeInterviewError(w, err, "интервью не найдено в этом исследовании")
		return
	}
	messages, err := pg.ListInterviewMessages(r.Context(), runID, interviewID)
	if err != nil {
		s.writeInterviewError(w, err, "не удалось прочитать историю интервью")
		return
	}
	if messages == nil {
		messages = []chat.Message{}
	}
	writeJSON(w, http.StatusOK, messages)
}

// writeInterviewError maps the interview store errors onto HTTP responses.
func (s *Server) writeInterviewError(w http.ResponseWriter, err error, message string) {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, chat.ErrNotFound):
		writeErr(w, http.StatusNotFound, "NOT_FOUND", message)
	case errors.Is(err, ErrServiceUnavailable):
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "интервью требуют PostgreSQL")
	default:
		logging.Logger.Error("interview_store_failed", "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", message)
	}
}
