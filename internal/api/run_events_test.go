package api

import (
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/sse"
	"testing"
)

func TestRunEventsFollowPersistedStages(t *testing.T) {
	store := newMemStore()
	run, _ := store.CreateRun("Роль")
	events := sse.NewMemEventStore(10)
	wrapper := eventRunStore{store, events}
	run.Stage = domain.StageAnalysis
	run.Status = domain.RunRunning
	if err := wrapper.Save(run); err != nil {
		t.Fatal(err)
	}
	run.Stage = domain.StageDone
	run.Status = domain.RunDone
	if err := wrapper.Save(run); err != nil {
		t.Fatal(err)
	}
	list, err := events.GetAfter("", 10)
	if err != nil || len(list) != 2 || list[0].Type != sse.EventStageChange || list[1].Type != sse.EventDone {
		t.Fatalf("%+v %v", list, err)
	}
	list, err = events.GetAfter(list[1].ID, 10)
	if err != nil || len(list) != 0 {
		t.Fatalf("после последнего события не должно быть повторов: %+v %v", list, err)
	}
}
