package database_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestRunInterviewsPostgres проверяет модель «несколько респондентов в одном
// исследовании» (карточка 12 плана MVP): у исследования может быть несколько
// интервью, активное ровно одно, а завершённое сохраняет своего респондента.
func TestRunInterviewsPostgres(t *testing.T) {
	url := os.Getenv("STORE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set STORE_TEST_DATABASE_URL to run PostgreSQL integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var runID string
	if err = pool.QueryRow(ctx,
		`INSERT INTO runs(profession, status, type) VALUES('Аналитик данных', 'running', 'chat') RETURNING id::text`).
		Scan(&runID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM runs WHERE id = $1`, runID)
	})

	interviews := store.NewChatSessionStore(pool)
	first, err := interviews.StartInterview(ctx, runID, domain.Respondent{Name: "Респондент А", Role: "аналитик данных"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != chat.InterviewActive {
		t.Fatalf("первое интервью должно быть активным, получено %q", first.Status)
	}
	if first.RespondentName != "Респондент А" || first.RespondentRole != "аналитик данных" {
		t.Fatalf("респондент не сохранён: %+v", first)
	}

	second, err := interviews.StartInterview(ctx, runID, domain.Respondent{Name: "Респондент Б"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatal("второе интервью должно получить собственный идентификатор")
	}

	list, err := interviews.ListInterviews(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("ожидалось 2 интервью, получено %d", len(list))
	}
	if list[0].ID != first.ID || list[0].Status != chat.InterviewCompleted {
		t.Fatalf("первое интервью должно быть завершено: %+v", list[0])
	}
	if list[1].ID != second.ID || list[1].Status != chat.InterviewActive {
		t.Fatalf("второе интервью должно быть активным: %+v", list[1])
	}

	// Ответы не смешиваются: история нового интервью ещё пуста, а чужой
	// идентификатор интервью не находится в этом исследовании.
	messages, err := interviews.InterviewMessages(ctx, runID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("у нового интервью не должно быть сообщений, получено %d", len(messages))
	}
	if _, err = interviews.GetInterview(ctx, runID, first.ID); err != nil {
		t.Fatalf("первое интервью должно оставаться доступным: %v", err)
	}
	if _, err = interviews.GetInterview(ctx, "00000000-0000-0000-0000-000000000000", second.ID); !errors.Is(err, chat.ErrNotFound) {
		t.Fatalf("чужое исследование должно давать ErrNotFound, получено %v", err)
	}
}
