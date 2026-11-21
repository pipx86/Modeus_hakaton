package chat

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llmobs"
)

// Errors of the repeatable finalization (POST /runs/{id}/retry). They are
// exported so the HTTP layer maps every precondition to its own status code
// instead of returning a generic 500.
var (
	// ErrNothingToRetry means the run has no persisted dialog to analyse.
	ErrNothingToRetry = errors.New("chat: nothing to retry")
	// ErrTreeAlreadyExists means the run already has a saved activity tree, so
	// repeating the pipeline would duplicate a finished research.
	ErrTreeAlreadyExists = errors.New("chat: run tree already exists")
	// ErrFinalizerUnavailable means the service has no post-interview pipeline
	// configured (for example no LLM analyst), so finalization cannot start.
	ErrFinalizerUnavailable = errors.New("chat: finalizer is not configured")
	// ErrRetryInProgress means the interview or another retry of this run is
	// still running, so a second finalization would duplicate the work.
	ErrRetryInProgress = errors.New("chat: finalization retry is already in progress")
)

// retryPlan is the read-only precondition snapshot of one repeatable
// finalization: what to analyse and which durable snapshot version to advance.
type retryPlan struct {
	runID      string
	profession string
	snapshot   *SessionSnapshot
	session    domain.InterviewSession
	store      SessionStore
	// version is the optimistic token the retry's own SaveStep must send. It is
	// bumped once the terminal snapshot is reset to active.
	version int64
}

// CheckRetryFinalize reports whether RetryFinalize can start, without changing
// any state. The HTTP layer uses it to answer 404/409/501 synchronously before
// it hands the multi-minute pipeline to the background.
func (s *Service) CheckRetryFinalize(ctx context.Context, runID string) error {
	_, err := s.planRetry(ctx, runID)
	return err
}

// RetryFinalize re-runs the post-interview pipeline for a run whose interview
// is already over but whose tree was never built. Unlike complete it does not
// need a live interviewer: the transcript is reconstructed from the durable
// dialog (SessionStore.ListMessages), which is the record the respondent
// actually sees.
//
// The terminal durable state is reset to active first, so the retry is always
// repeatable: a failed finalizer leaves the run failed again, without any
// "already retried" flag, and a later call can analyse the same transcript.
// It returns ErrTreeAlreadyExists when the tree is already saved and
// ErrNothingToRetry when there is no persisted dialog at all.
func (s *Service) RetryFinalize(ctx context.Context, runID string) error {
	plan, err := s.planRetry(ctx, runID)
	if err != nil {
		return err
	}
	// Two retries of the same run in one process would run the analyst twice
	// and race on the tree: reject the second one explicitly.
	if !s.beginRetryFinalize(plan.runID) {
		return ErrRetryInProgress
	}
	defer s.endRetryFinalize(plan.runID)

	if err := s.resetFinalizedSession(ctx, plan); err != nil {
		return err
	}
	// The history stays readable, but no request may rebuild a live interviewer
	// session while the background pipeline owns the run.
	s.forgetSession(plan.runID)

	return s.runRetryFinalize(ctx, plan)
}

// planRetry loads and validates the durable state of a retry. It never writes:
// CheckRetryFinalize and RetryFinalize share it, so the HTTP preconditions and
// the actual retry cannot drift apart.
func (s *Service) planRetry(ctx context.Context, runID string) (*retryPlan, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, ErrNotFound
	}
	if s.finalizerFn() == nil {
		return nil, ErrFinalizerUnavailable
	}
	store := s.sessionStoreOf()
	if store == nil {
		// Without a durable session there is no persisted dialog to rebuild.
		return nil, ErrNotFound
	}

	ctx, cancel := context.WithTimeout(ctx, persistTimeout)
	defer cancel()

	plan := &retryPlan{runID: runID, store: store}
	snapshot, err := store.LoadSession(ctx, runID)
	switch {
	case err == nil:
		plan.snapshot = &snapshot
		plan.profession = strings.TrimSpace(snapshot.Profession)
		plan.version = snapshot.Version
	case errors.Is(err, ErrNotFound):
		// A run created before durable sessions keeps its history only. It has
		// no snapshot to reset and no profession to pass to the analyst, but the
		// persisted transcript can still be analysed.
	default:
		return nil, fmt.Errorf("chat: load session: %w", err)
	}

	runStatus, err := store.RunStatus(ctx, runID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("chat: load run status: %w", err)
	}
	runFinished := isTerminalRunStatus(runStatus)
	if plan.snapshot == nil {
		// A legacy run has no durable cursor: only an already finished run can
		// be re-analysed, otherwise a live interview would be duplicated.
		if !runFinished {
			return nil, ErrRetryInProgress
		}
	} else if !runFinished && !isTerminalSessionStatus(plan.snapshot.Status) {
		// The interviewer is still collecting answers (or another finalization
		// is running): there is no complete transcript to retry yet.
		return nil, ErrRetryInProgress
	}

	hasTree, err := runHasTreeForRetry(ctx, store, runID)
	if err != nil {
		return nil, err
	}
	if hasTree {
		return nil, ErrTreeAlreadyExists
	}

	messages, err := store.ListMessages(ctx, runID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("chat: load messages: %w", err)
	}
	if len(messages) == 0 {
		return nil, ErrNothingToRetry
	}
	plan.session = sessionFromMessages(runID, plan.profession, messages)
	if plan.snapshot != nil {
		// The interviewer snapshot is not used as the transcript (the dialog is
		// authoritative), but it still carries the GitHub profile context that
		// the learning-plan finalizer prepends to the transcript.
		state := plan.snapshot.Interviewer.State
		plan.session.ProfileContext = state.ProfileContext
		plan.session.ProfileSkills = append([]string(nil), state.ProfileSkills...)
	}
	return plan, nil
}

// resetFinalizedSession flips the terminal durable session back to active before
// the pipeline runs again. The interviewer snapshot itself is kept, so the run
// still has its durable cursor if the retry is interrupted.
func (s *Service) resetFinalizedSession(ctx context.Context, plan *retryPlan) error {
	if plan.snapshot == nil {
		// Legacy run: there is no durable session row to reset.
		return nil
	}
	snapshot := *plan.snapshot
	snapshot.Status = SessionActive
	snapshot.FinalStatus = ""
	if snapshot.SchemaVersion == 0 {
		snapshot.SchemaVersion = SessionSchemaVersion
	}
	if plan.profession == "" {
		// The table requires a non-empty profession; without it the row can
		// neither be reset nor re-created. The transcript is still analysed and
		// the run status is still updated.
		log.Printf("chat: retry run %s: session has no profession, durable reset skipped", plan.runID)
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, persistTimeout)
	defer cancel()
	if err := plan.store.SaveStep(ctx, plan.runID, snapshot, nil); err != nil {
		return fmt.Errorf("chat: reset session for retry: %w", err)
	}
	plan.version = snapshot.Version + 1
	return nil
}

// runRetryFinalize runs the pipeline on the persisted transcript the same way
// complete does, but without a live interviewer. The final run status is always
// recorded, so a failed attempt can be retried again.
func (s *Service) runRetryFinalize(ctx context.Context, plan *retryPlan) error {
	finalizer := s.finalizerFn()
	if finalizer == nil {
		return ErrFinalizerUnavailable
	}
	analysisCtx, cancel := context.WithTimeout(ctx, finalizerTimeout)
	analysisCtx = llmobs.WithRunID(analysisCtx, plan.runID)
	finalStatus, err := finalizer(analysisCtx, plan.runID, plan.profession, plan.session)
	cancel()

	status := domain.RunDone
	if err != nil {
		log.Printf("chat: retry finalizer failed for run %s: %v", plan.runID, err)
		status = domain.RunFailed
		// A previous attempt may have committed the tree before it failed; the
		// saved tree is the authoritative success signal, same as complete.
		treeCtx, treeCancel := context.WithTimeout(context.Background(), persistTimeout)
		hasTree, treeErr := runHasTreeForRetry(treeCtx, plan.store, plan.runID)
		treeCancel()
		if treeErr == nil && hasTree {
			log.Printf("chat: retry run %s already has a saved tree; keeping done status", plan.runID)
			status = domain.RunDone
		}
	} else if finalStatus != "" {
		status = finalStatus
	}

	// The run row is updated before the terminal snapshot: a crash in between
	// leaves a finished run with an "active" session, which still reads as
	// finished and can be retried, while the opposite order could leave a
	// "failed" run with a saved tree.
	s.notifyFinish(plan.runID, status)
	s.persistRetryResult(plan, status)

	if err != nil {
		return fmt.Errorf("chat: retry finalization failed for run %s: %w", plan.runID, err)
	}
	return nil
}

// persistRetryResult writes the terminal chat session snapshot of a retried
// finalization. A write failure is logged but not returned: the run status was
// already recorded, and the retry stays possible because nothing is marked as
// permanently final.
func (s *Service) persistRetryResult(plan *retryPlan, status domain.RunStatus) {
	if plan.snapshot == nil || plan.profession == "" {
		return
	}
	snapshot := *plan.snapshot
	snapshot.Status = SessionCompleted
	if status == domain.RunFailed {
		snapshot.Status = SessionFailed
	}
	snapshot.FinalStatus = status
	snapshot.Version = plan.version

	ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
	defer cancel()
	if err := plan.store.SaveStep(ctx, plan.runID, snapshot, nil); err != nil {
		log.Printf("chat: persist retry result for run %s: %v", plan.runID, err)
	}
}

// sessionFromMessages rebuilds the interview state the finalizer analyses from
// the persisted dialog. The dialog is the authoritative record of what the
// respondent said: the durable session snapshot of a finished run is terminal,
// and the in-memory interviewer is long gone, but every question and answer was
// written through SessionStore.SaveStep.
//
// Only answered questions are reconstructed: an agent message is a question
// when a user message follows it. The greeting and the closing status messages
// are also agent messages, so pairing them blindly would shift the whole
// question/answer chain; they are superseded by the next real question and
// never reach the transcript.
func sessionFromMessages(runID, profession string, messages []Message) domain.InterviewSession {
	session := domain.InterviewSession{
		ID: "run-" + runID,
		Respondent: domain.Respondent{
			ID:         "respondent-" + runID,
			Role:       profession,
			Profession: profession,
		},
		Profession: profession,
		Status:     domain.SessionDone,
	}
	var pending *domain.Question
	for _, message := range messages {
		text := strings.TrimSpace(message.Content)
		if text == "" {
			continue
		}
		switch message.Role {
		case "agent":
			// A newer question replaces a previous one without an answer (the
			// greeting, or a question cut off by a restart). Only answered
			// questions carry interview evidence into the analysis.
			pending = &domain.Question{
				ID:    fmt.Sprintf("q-%d", message.Sequence),
				Level: domain.QuestionRole,
				Text:  text,
			}
		case "user":
			if pending == nil {
				// Nothing was asked yet: a stray answer cannot become evidence.
				continue
			}
			session.Questions = append(session.Questions, *pending)
			session.Answers = append(session.Answers, domain.Answer{
				QuestionID: pending.ID,
				Text:       text,
			})
			pending = nil
		}
	}
	return session
}

// isTerminalSessionStatus reports whether the durable chat session is over.
func isTerminalSessionStatus(status SessionStatus) bool {
	return status == SessionCompleted || status == SessionFailed
}

// runHasTreeForRetry reports whether the finalizer already saved a tree. The
// capability is optional: adapters that cannot answer it rely on the finalizer
// itself rejecting a second SaveTree.
func runHasTreeForRetry(ctx context.Context, store SessionStore, runID string) (bool, error) {
	checker, ok := store.(runTreeStore)
	if !ok {
		return false, nil
	}
	hasTree, err := checker.RunHasTree(ctx, runID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, ErrNotFound
		}
		return false, fmt.Errorf("chat: check run tree: %w", err)
	}
	return hasTree, nil
}

// beginRetryFinalize claims the in-process retry slot of a run.
func (s *Service) beginRetryFinalize(runID string) bool {
	s.retryMu.Lock()
	defer s.retryMu.Unlock()
	if s.retrying == nil {
		s.retrying = make(map[string]bool)
	}
	if s.retrying[runID] {
		return false
	}
	s.retrying[runID] = true
	return true
}

// endRetryFinalize releases the in-process retry slot of a run.
func (s *Service) endRetryFinalize(runID string) {
	s.retryMu.Lock()
	delete(s.retrying, runID)
	s.retryMu.Unlock()
}

// retryFinalizeInFlight reports whether this process is repeating the
// finalization of the run right now.
func (s *Service) retryFinalizeInFlight(runID string) bool {
	s.retryMu.Lock()
	defer s.retryMu.Unlock()
	return s.retrying[runID]
}
