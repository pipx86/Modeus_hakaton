package api

import (
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/sse"
)

// eventRunStore публикует этап только после успешной записи состояния.
type eventRunStore struct {
	store  domain.Store
	events sse.EventStore
}

func (s eventRunStore) Save(run *domain.Run) error {
	if err := s.store.Save(run); err != nil {
		return err
	}
	typ := sse.EventStageChange
	if run.Stage == domain.StageDone {
		typ = sse.EventDone
	}
	if run.Stage == domain.StageFailed {
		typ = sse.EventError
	}
	return s.events.Append(sse.NewEvent(run.ID, typ, map[string]any{"stage": run.Stage, "status": run.Status, "pct": stagePct[run.Stage]}))
}

func (s *Server) AppendRunStatusEvent(runID string, status domain.RunStatus) error {
	typ := sse.EventStageChange
	stage := domain.StageInterview
	switch status {
	case domain.RunDone:
		typ = sse.EventDone
		stage = domain.StageDone
	case domain.RunDegraded:
		typ = sse.EventDone
		stage = domain.StageDone
	case domain.RunFailed:
		typ = sse.EventError
		stage = domain.StageFailed
	}
	return s.eventStore.Append(sse.NewEvent(runID, typ, map[string]any{
		"stage":  stage,
		"status": status,
		"pct":    stagePct[stage],
	}))
}
