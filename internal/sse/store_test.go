package sse

import (
	"testing"
)

func TestMemEventStore_AppendAndGetAfter(t *testing.T) {
	store := NewMemEventStore(10)

	e1 := NewEvent("run-1", EventStageChange, map[string]string{"stage": "interview"})
	e2 := NewEvent("run-1", EventStageChange, map[string]string{"stage": "analysis"})
	e3 := NewEvent("run-1", EventDone, map[string]string{"status": "completed"})

	_ = store.Append(e1)
	_ = store.Append(e2)
	_ = store.Append(e3)

	got, err := store.GetAfter(e1.ID, 100)
	if err != nil {
		t.Fatalf("GetAfter failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ожидали 2 события после e1, получили %d", len(got))
	}
	if got[0].ID != e2.ID || got[1].ID != e3.ID {
		t.Fatalf("порядок replay нарушен: [%s, %s]", got[0].ID, got[1].ID)
	}
}

func TestMemEventStore_RingBufferOverflow(t *testing.T) {
	store := NewMemEventStore(3)

	var ids []string
	for i := 0; i < 5; i++ {
		ev := NewEvent("run-x", EventProgress, i)
		_ = store.Append(ev)
		ids = append(ids, ev.ID)
	}

	all, _ := store.GetAfter("", 100)
	if len(all) != 3 {
		t.Fatalf("после overflow ожидали 3 события, получили %d", len(all))
	}
	if all[0].ID != ids[2] || all[1].ID != ids[3] || all[2].ID != ids[4] {
		t.Fatalf("кольцо сохранило неправильные элементы: %v", extractIDs(all))
	}
}

func TestMemEventStore_GetAfterUnknownIDReturnsFullHistory(t *testing.T) {
	store := NewMemEventStore(10)
	e1 := NewEvent("r", EventStageChange, nil)
	e2 := NewEvent("r", EventDone, nil)
	_ = store.Append(e1)
	_ = store.Append(e2)

	got, _ := store.GetAfter("nonexistent-id", 100)
	if len(got) != 2 {
		t.Fatalf("unknown lastID должен вернуть всю историю, получили %d", len(got))
	}
}

func TestSSEEvent_EncodeFormat(t *testing.T) {
	ev := &SSEEvent{
		ID:      "evt-test-1",
		Type:    EventStageChange,
		RunID:   "run-abc",
		Payload: map[string]string{"stage": "analysis"},
	}
	data, err := ev.Encode()
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	expected := "id: evt-test-1\nevent: stage_change\ndata: {\"stage\":\"analysis\"}\n\n"
	if string(data) != expected {
		t.Fatalf("формат SSE неверен:\nполучили: %q\nожидали: %q", string(data), expected)
	}
}

func TestMemEventStore_SubscribeLive(t *testing.T) {
	store := NewMemEventStore(10)
	received := make(chan *SSEEvent, 5)

	unsub := store.SubscribeLive(func(ev *SSEEvent) {
		select {
		case received <- ev:
		default:
		}
	})
	defer unsub()

	e := NewEvent("run-live", EventProgress, "tick")
	_ = store.Append(e)

	got := <-received
	if got.ID != e.ID {
		t.Fatalf("подписчик получил не то событие: want %s got %s", e.ID, got.ID)
	}
}

func extractIDs(events []*SSEEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.ID
	}
	return out
}
