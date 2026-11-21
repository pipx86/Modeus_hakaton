package chat

import (
	"errors"
	"fmt"

	"activity-descriptor/internal/agent/interviewer"
)

type InterviewProgress struct {
	interviewer.Progress
	AwaitingContinuation bool `json:"awaiting_continuation,omitempty"`
	Finishing            bool `json:"finishing,omitempty"`
}

// DecideContinuation saves the respondent's choice before starting the next step.
func (s *Service) DecideContinuation(runID string, continueInterview bool) error {
	sess, err := s.lookup(runID)
	if err != nil {
		return err
	}

	sess.mu.Lock()
	if !sess.awaitingContinuation {
		sess.mu.Unlock()
		return ErrContinuationUnavailable
	}
	before := sess.session.Snapshot()
	status := SessionActive
	if continueInterview {
		sess.session.Extend()
	} else {
		sess.session.Close()
		status = SessionFinalizing
	}
	snapshot := s.snapshotLocked(sess, status, "")
	snapshot.AwaitingContinuation = false
	if err := s.persistStep(sess, snapshot, nil); err != nil {
		_ = sess.session.Restore(before)
		if errors.Is(err, ErrVersionConflict) {
			s.forgetSession(runID)
		}
		sess.mu.Unlock()
		return fmt.Errorf("chat: persist continuation choice: %w", err)
	}

	sess.awaitingContinuation = false
	sess.asking = continueInterview
	sess.finalizing = !continueInterview
	sess.mu.Unlock()
	s.publish(runID, Event{Type: "interview_progress"})
	s.publish(runID, Event{Type: "agent_thinking"})
	if continueInterview {
		go s.askNext(sess)
	} else {
		go s.complete(sess)
	}
	return nil
}
