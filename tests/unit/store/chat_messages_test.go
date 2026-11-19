package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v4"
)

func TestChatMessageStorePreservesTranscript(t *testing.T) {
	pool, _ := testStore(t)
	messages := store.NewChatMessageStore(pool)
	id := testUUID(30)
	runID := id.String()
	original := chat.Message{
		ID:        "msg-1",
		Role:      "user",
		Content:   "  О'Нил: \"цитата\" <mark data-proof='literal'>& 3 < 7 \\ путь\n  ",
		CreatedAt: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC),
		Sequence:  1,
		Citations: []chat.Citation{{Title: "Интервью", Quote: "<выдержка> & текст"}},
	}
	citations, err := json.Marshal(original.Citations)
	if err != nil {
		t.Fatal(err)
	}
	when := pgtype.Timestamptz{Time: original.CreatedAt, Valid: true}
	for _, updated := range []int64{1, 0} {
		pool.ExpectExec("InsertRunChatMessage").WithArgs(original.ID, id, original.Role, original.Content,
			int32(original.Sequence), citations, when).WillReturnResult(pgxmock.NewResult("INSERT", updated))
		if err := messages.AppendMessage(context.Background(), runID, original); err != nil {
			t.Fatal(err)
		}
	}
	rows := pgxmock.NewRows(chatMessageColumns()).AddRow(original.ID, id, original.Role, original.Content,
		int32(original.Sequence), citations, when)
	pool.ExpectQuery("ListRunChatMessages").WithArgs(id).WillReturnRows(rows)
	got, err := messages.ListMessages(context.Background(), runID)
	if err != nil || !reflect.DeepEqual(got, []chat.Message{original}) {
		t.Fatalf("сообщения: %#v, ошибка: %v", got, err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestChatMessageStoreValidationAndReadErrors(t *testing.T) {
	pool, _ := testStore(t)
	messages := store.NewChatMessageStore(pool)
	ctx := context.Background()
	id := testUUID(30)
	runID := id.String()
	temporary := errors.New("test store unavailable")

	if err := messages.AppendMessage(ctx, "bad-id", chat.Message{Sequence: 1}); !errors.Is(err, chat.ErrNotFound) {
		t.Fatalf("запись с неверным ID: %v", err)
	}
	if err := messages.AppendMessage(ctx, runID, chat.Message{}); err == nil {
		t.Fatal("записано сообщение без номера")
	}
	if _, err := messages.ListMessages(ctx, "bad-id"); !errors.Is(err, chat.ErrNotFound) {
		t.Fatalf("чтение с неверным ID: %v", err)
	}
	pool.ExpectExec("InsertRunChatMessage").WithArgs("msg-2", id, "user", "ответ", int32(2), []byte(nil), pgxmock.AnyArg()).WillReturnError(temporary)
	if err := messages.AppendMessage(ctx, runID, chat.Message{ID: "msg-2", Role: "user", Content: "ответ", Sequence: 2}); !errors.Is(err, temporary) {
		t.Fatalf("ошибка записи потеряна: %v", err)
	}
	pool.ExpectQuery("ListRunChatMessages").WithArgs(id).WillReturnError(temporary)
	if _, err := messages.ListMessages(ctx, runID); !errors.Is(err, temporary) {
		t.Fatalf("ошибка чтения потеряна: %v", err)
	}
	bad := pgxmock.NewRows(chatMessageColumns()).AddRow("msg-3", id, "agent", "цитата", int32(3), []byte("["), pgtype.Timestamptz{})
	pool.ExpectQuery("ListRunChatMessages").WithArgs(id).WillReturnRows(bad)
	if _, err := messages.ListMessages(ctx, runID); err == nil {
		t.Fatal("некорректный JSON цитат прочитан без ошибки")
	}
	pool.ExpectQuery("ListRunChatMessages").WithArgs(id).WillReturnRows(pgxmock.NewRows(chatMessageColumns()))
	pool.ExpectQuery("RunExists").WithArgs(id).WillReturnError(temporary)
	if _, err := messages.ListMessages(ctx, runID); !errors.Is(err, temporary) {
		t.Fatalf("ошибка проверки прогона потеряна: %v", err)
	}
	for _, exists := range []bool{true, false} {
		pool.ExpectQuery("ListRunChatMessages").WithArgs(id).WillReturnRows(pgxmock.NewRows(chatMessageColumns()))
		pool.ExpectQuery("RunExists").WithArgs(id).WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(exists))
		got, err := messages.ListMessages(ctx, runID)
		if exists {
			if err != nil || got == nil || len(got) > 0 {
				t.Fatalf("пустой диалог существующего прогона: %#v, %v", got, err)
			}
		} else if !errors.Is(err, chat.ErrNotFound) {
			t.Fatalf("диалог отсутствующего прогона: %v", err)
		}
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func chatMessageColumns() []string {
	return []string{"id", "run_id", "role", "content", "sequence", "citations", "created_at"}
}
