package sse_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"activity-descriptor/internal/sse"

	"github.com/pashagolub/pgxmock/v4"
)

func TestPostgresEventsWriteAndResume(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer pool.Close()
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})
	events := sse.NewPGEventStore(pool)
	payload := json.RawMessage(`{"stage":"analysis"}`)
	event := sse.SSEEvent{
		ID: "after-1", RunID: "run-fixture", Type: sse.EventStageChange, Payload: payload,
		Timestamp: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC),
	}
	errWrite := errors.New("test event insert failed")
	pool.ExpectBegin()
	pool.ExpectExec("pg_advisory_xact_lock").WillReturnResult(pgxmock.NewResult("SELECT", 1))
	pool.ExpectExec("INSERT INTO run_sse_events").WithArgs(event.ID, event.RunID, event.Type, []byte(payload), event.Timestamp).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()
	if err = events.Append(&event); err != nil {
		t.Fatal(err)
	}
	pool.ExpectBegin()
	pool.ExpectExec("pg_advisory_xact_lock").WillReturnResult(pgxmock.NewResult("SELECT", 1))
	pool.ExpectExec("INSERT INTO run_sse_events").WithArgs(event.ID, event.RunID, event.Type, []byte(payload), event.Timestamp).
		WillReturnError(errWrite)
	pool.ExpectRollback()
	if err = events.Append(&event); !errors.Is(err, errWrite) {
		t.Fatalf("ошибка записи события: %v", err)
	}
	pool.ExpectQuery("WHERE.*run_id::text").WithArgs(event.RunID, "after-0", 100).
		WillReturnRows(pgxmock.NewRows([]string{"event_id", "run_id", "event_type", "payload", "occurred_at"}).
			AddRow(event.ID, event.RunID, event.Type, []byte(payload), event.Timestamp))
	got, err := events.GetRunAfter(event.RunID, "after-0", 0)
	if err != nil || len(got) != 1 || got[0].ID != event.ID || got[0].Type != event.Type ||
		string(got[0].Payload.(json.RawMessage)) != string(payload) || !got[0].Timestamp.Equal(event.Timestamp) {
		t.Fatalf("страница событий: %#v, %v", got, err)
	}
	pool.ExpectQuery("FROM run_sse_events").WithArgs("", "after-1", 1).WillReturnError(errWrite)
	if _, err = events.GetAfter("after-1", 1); !errors.Is(err, errWrite) {
		t.Fatalf("ошибка чтения истории: %v", err)
	}
}

func TestPostgresEventsRejectUnserializablePayloadBeforeWriting(t *testing.T) {
	store := sse.NewPGEventStore(nil)
	if err := store.Append(&sse.SSEEvent{Payload: make(chan int)}); err == nil {
		t.Fatal("получилась запись несериализуемого события")
	}
}
