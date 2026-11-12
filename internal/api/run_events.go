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
