package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
)

// persistentInterviewer is a deterministic llm.ChatClient for the durable chat
// tests: it always returns a valid interview question, so the dialog advances
// without a real model.
type persistentInterviewer struct{}

func (persistentInterviewer) Chat(_ context.Context, _ []llm.Message) (string, error) {
	return `{"question":{"id":"q","level":"role","text":"Расскажите о вашей роли."},"rationale":"Старт интервью.","is_clarification":false}`, nil
}

func newPersistentChatService(pool *pgxpool.Pool) *chat.Service {
	service := chat.NewService(persistentInterviewer{}, nil)
	service.SetMessageStore(store.NewChatMessageStore(pool))
	return service
}

func getChatMessages(t *testing.T, handler http.Handler, runID, role string) (int, []chat.Message) {
	t.Helper()
	rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+runID+"/messages", role, "")
	if rec.Code != http.StatusOK {
		return rec.Code, nil
	}
	var messages []chat.Message
	if err := json.Unmarshal(rec.Body.Bytes(), &messages); err != nil {
		t.Fatalf("messages response is not JSON: %v (%s)", err, rec.Body.String())
	}
	return rec.Code, messages
}

func waitForChatMessages(t *testing.T, handler http.Handler, runID string, want int) []chat.Message {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		code, messages := getChatMessages(t, handler, runID, "student")
		if code == http.StatusOK && len(messages) >= want {
			return messages
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("GET /runs/%s/messages did not return %d messages in time", runID, want)
	return nil
}

func sameChatMessage(a, b chat.Message) bool {
	return a.ID == b.ID && a.Role == b.Role && a.Content == b.Content && a.CreatedAt.Equal(b.CreatedAt)
}

// TestChatHistorySurvivesRestart is the end-to-end check for durable chat: a
// real PostgreSQL keeps the greeting, questions, answers and the final courses
// message, and a fresh chat.Service (a restarted backend) serves them through
// the unchanged GET /api/v1/runs/{id}/messages contract.
func TestChatHistorySurvivesRestart(t *testing.T) {
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая БД")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	// Cleanup functions run LIFO, so the run is deleted before the pool closes.
	t.Cleanup(pool.Close)
	var migrated bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('run_chat_messages') IS NOT NULL`).Scan(&migrated); err != nil || !migrated {
		t.Skip("миграция run_chat_messages не применена в тестовой БД")
	}

	pg := NewPGStore(pool)
	run, err := pg.CreateRun("Аналитик данных")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM runs WHERE id=$1`, run.ID)
	})
	if err := pg.SetRunType(run.ID, domain.RunTypeChat); err != nil {
		t.Fatal(err)
	}
	if err := pg.SetStage(run.ID, domain.StageInterview); err != nil {
		t.Fatal(err)
	}

	service := newPersistentChatService(pool)
	handler := NewServerWithChat(pg, service).Routes()
	if err := service.Start(run.ID, run.Profession); err != nil {
		t.Fatalf("Start: %v", err)
	}
	path := "/api/v1/runs/" + run.ID + "/messages"

	greeting := waitForChatMessages(t, handler, run.ID, 2)
	if greeting[0].Role != "agent" || strings.TrimSpace(greeting[0].Content) == "" {
		t.Fatalf("first message is not the agent greeting: %+v", greeting[0])
	}
	if greeting[0].Role == greeting[1].Role && greeting[1].Role == "user" {
		t.Fatalf("unexpected dialog start: %+v", greeting[:2])
	}

	// Role behaviour must stay unchanged: guests may read the history but not
	// write to it.
	if rec := do(t, handler, http.MethodGet, path, "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous GET = %d, want 401", rec.Code)
	}
	if code, _ := getChatMessages(t, handler, run.ID, "guest"); code != http.StatusOK {
		t.Fatalf("guest GET = %d, want 200", code)
	}
	if rec := do(t, handler, http.MethodPost, path, "guest", `{"content":"нельзя"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("guest POST = %d, want 403", rec.Code)
	}

	// An active run still accepts answers through POST and persists them once.
	answer := "Я аналитик данных и отвечаю за управленческую отчётность."
	posted := do(t, handler, http.MethodPost, path, "teacher", `{"content":`+quoteJSON(answer)+`}`)
	if posted.Code != http.StatusCreated {
		t.Fatalf("POST message = %d: %s", posted.Code, posted.Body.String())
	}
	var userMessage chat.Message
	if err := json.Unmarshal(posted.Body.Bytes(), &userMessage); err != nil {
		t.Fatalf("POST response is not a chat message: %v (%s)", err, posted.Body.String())
	}
	if userMessage.Role != "user" || userMessage.Content != answer {
		t.Fatalf("unexpected user message: %+v", userMessage)
	}
	var userRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM run_chat_messages WHERE run_id=$1 AND role='user' AND content=$2`, run.ID, answer).Scan(&userRows); err != nil {
		t.Fatal(err)
	}
	if userRows != 1 {
		t.Fatalf("user answer stored %d times, want 1", userRows)
	}

	// Five more answers exhaust the six-question plan and trigger the final
	// courses message, which must also be persisted.
	for answerIndex := 1; answerIndex < 6; answerIndex++ {
		waitForChatMessages(t, handler, run.ID, 2*answerIndex+2)
		rec := do(t, handler, http.MethodPost, path, "teacher", `{"content":"Конкретный ответ про аналитику данных."}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST answer %d = %d: %s", answerIndex, rec.Code, rec.Body.String())
		}
	}
	final := waitForChatMessages(t, handler, run.ID, 14)
	last := final[len(final)-1]
	if last.Role != "agent" || !strings.Contains(last.Content, "Интервью завершено") || !strings.Contains(last.Content, "https://stepik.org/course/300883/promo") {
		t.Fatalf("final courses message was not persisted: %+v", last)
	}

	// The store is idempotent per (run_id, sequence): a retried write is a no-op.
	adapter := store.NewChatMessageStore(pool)
	var rowsBefore int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM run_chat_messages WHERE run_id=$1`, run.ID).Scan(&rowsBefore); err != nil {
		t.Fatal(err)
	}
	stored, err := adapter.ListMessages(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(stored) != rowsBefore {
		t.Fatalf("ListMessages returned %d rows, database has %d", len(stored), rowsBefore)
	}
	if err := adapter.AppendMessage(ctx, run.ID, stored[0]); err != nil {
		t.Fatalf("re-AppendMessage: %v", err)
	}
	var rowsAfter int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM run_chat_messages WHERE run_id=$1`, run.ID).Scan(&rowsAfter); err != nil {
		t.Fatal(err)
	}
	if rowsAfter != rowsBefore {
		t.Fatalf("retried AppendMessage duplicated rows: %d -> %d", rowsBefore, rowsAfter)
	}

	// Simulate a backend restart: a brand-new chat.Service has no in-memory
	// sessions, so GET must fall back to PostgreSQL.
	restarted := newPersistentChatService(pool)
	restartedHandler := NewServerWithChat(pg, restarted).Routes()
	beforeRestart := final
	afterRestart := waitForChatMessages(t, restartedHandler, run.ID, len(beforeRestart))
	if len(afterRestart) != len(beforeRestart) {
		t.Fatalf("after restart %d messages, before %d", len(afterRestart), len(beforeRestart))
	}
	for index := range beforeRestart {
		if !sameChatMessage(beforeRestart[index], afterRestart[index]) {
			t.Fatalf("message %d changed after restart: before %+v, after %+v", index, beforeRestart[index], afterRestart[index])
		}
	}
	// The persisted order is the dialog order: greeting, first question, then
	// one user answer and one agent question per turn, then the final message.
	for index, message := range afterRestart {
		wantRole := "agent"
		if index > 1 && index%2 == 0 {
			wantRole = "user"
		}
		if message.Role != wantRole {
			t.Fatalf("message %d role = %q, want %q (%+v)", index, message.Role, wantRole, message)
		}
	}

	// Unknown runs still return 404 with the documented error code.
	var unknownRunID string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&unknownRunID); err != nil {
		t.Fatal(err)
	}
	rec := do(t, restartedHandler, http.MethodGet, "/api/v1/runs/"+unknownRunID+"/messages", "student", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown run GET = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != "NOT_FOUND" {
		t.Fatalf("unknown run body = %s, want NOT_FOUND", rec.Body.String())
	}
}

// quoteJSON encodes s as a JSON string literal (without importing fmt just for
// the small test payloads).
func quoteJSON(s string) string {
	encoded, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
