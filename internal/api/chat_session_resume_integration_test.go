package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
)

// requireChatSessionSchema skips the test when the test database does not have
// the durable chat migrations applied.
func requireChatSessionSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var ready bool
	err := pool.QueryRow(context.Background(),
		`SELECT to_regclass('run_chat_messages') IS NOT NULL AND to_regclass('run_chat_sessions') IS NOT NULL`).Scan(&ready)
	if err != nil || !ready {
		t.Skip("в тестовой БД не применены миграции run_chat_messages/run_chat_sessions")
	}
}

// newSessionChatService builds a chat service backed by PostgreSQL snapshots.
// onFinish is used to mirror the run status transition, like cmd/server does.
func newSessionChatService(pool *pgxpool.Pool, onFinish func(string, domain.RunStatus)) *chat.Service {
	service := chat.NewService(persistentInterviewer{}, onFinish)
	service.SetSessionStore(store.NewChatSessionStore(pool))
	return service
}

// createDurableChatRun creates a running chat run and registers its cleanup.
func createDurableChatRun(t *testing.T, pg *PGStore, pool *pgxpool.Pool, profession string) string {
	t.Helper()
	run, err := pg.CreateRun(profession)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM runs WHERE id=$1`, run.ID)
	})
	if err := pg.SetRunType(run.ID, domain.RunTypeChat); err != nil {
		t.Fatalf("SetRunType: %v", err)
	}
	if err := pg.SetStage(run.ID, domain.StageInterview); err != nil {
		t.Fatalf("SetStage: %v", err)
	}
	return run.ID
}

// waitForRunStatus polls the runs row until it reaches want.
func waitForRunStatus(t *testing.T, pool *pgxpool.Pool, runID string, want domain.RunStatus) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(), `SELECT status FROM runs WHERE id=$1`, runID).Scan(&last); err == nil && last == string(want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s status = %q, want %q", runID, last, want)
}

func decodeErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var payload errorBody
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("response is not an error body: %v (%s)", err, body)
	}
	return payload.Code
}

// TestChatSessionResumesAfterBackendRestart is the end-to-end check for durable
// session restoration: a real PostgreSQL keeps the interviewer cursor, a fresh
// chat.Service (restarted backend) reloads it and the interview continues to the
// final Stepik message.
func TestChatSessionResumesAfterBackendRestart(t *testing.T) {
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая БД (INGEST_TEST_DATABASE_URL)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	requireChatSessionSchema(t, pool)

	pg := NewPGStore(pool)
	const profession = "Аналитик данных"
	runID := createDurableChatRun(t, pg, pool, profession)
	path := "/api/v1/runs/" + runID + "/messages"

	// First process: greet, ask the first question and accept one answer.
	first := newSessionChatService(pool, func(_ string, status domain.RunStatus) {
		_ = pg.SetStatus(runID, status)
	})
	firstHandler := NewServerWithChat(pg, first).Routes()
	if err := first.Start(runID, profession); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForChatMessages(t, firstHandler, runID, 2)
	posted := do(t, firstHandler, http.MethodPost, path, "teacher", `{"content":"Я аналитик данных и отвечаю за управленческую отчётность."}`)
	if posted.Code != http.StatusCreated {
		t.Fatalf("first POST = %d: %s", posted.Code, posted.Body.String())
	}
	beforeRestart := waitForChatMessages(t, firstHandler, runID, 4)

	// Restart: a new service has no in-memory sessions and loads the session
	// cursor from PostgreSQL.
	restarted := newSessionChatService(pool, func(_ string, status domain.RunStatus) {
		_ = pg.SetStatus(runID, status)
	})
	restartedHandler := NewServerWithChat(pg, restarted).Routes()
	history := waitForChatMessages(t, restartedHandler, runID, 4)
	if len(history) != len(beforeRestart) {
		t.Fatalf("after restart %d messages, want %d", len(history), len(beforeRestart))
	}
	// The existing message ids, timestamps and contents stay byte-identical.
	for index := range beforeRestart {
		if !sameChatMessage(beforeRestart[index], history[index]) {
			t.Fatalf("message %d changed after restart: before %+v, after %+v", index, beforeRestart[index], history[index])
		}
	}

	// Five answers remain; the restored session continues exactly where it
	// stopped and finishes the plan.
	for answerIndex := 0; answerIndex < 5; answerIndex++ {
		waitForChatMessages(t, restartedHandler, runID, 4+2*answerIndex)
		rec := do(t, restartedHandler, http.MethodPost, path, "teacher", `{"content":"Конкретный ответ про аналитику данных."}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST after restart %d = %d: %s", answerIndex, rec.Code, rec.Body.String())
		}
	}
	final := waitForChatMessages(t, restartedHandler, runID, 14)
	last := final[len(final)-1]
	if last.Role != "agent" || !strings.Contains(last.Content, "Интервью завершено") || !strings.Contains(last.Content, "«Дерево»") {
		t.Fatalf("final Stepik message was not persisted: %+v", last)
	}

	var sessionStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM run_chat_sessions WHERE run_id=$1`, runID).Scan(&sessionStatus); err != nil {
		t.Fatalf("read chat session status: %v", err)
	}
	if sessionStatus != string(chat.SessionCompleted) {
		t.Fatalf("chat session status = %q, want completed", sessionStatus)
	}
	waitForRunStatus(t, pool, runID, domain.RunDone)

	var messageRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM run_chat_messages WHERE run_id=$1`, runID).Scan(&messageRows); err != nil {
		t.Fatal(err)
	}
	if messageRows != 14 {
		t.Fatalf("stored %d messages, want 14", messageRows)
	}

	// A second restart serves the completed dialog and keeps it read-only.
	third := newSessionChatService(pool, nil)
	thirdHandler := NewServerWithChat(pg, third).Routes()
	readOnly := waitForChatMessages(t, thirdHandler, runID, 14)
	if len(readOnly) != 14 {
		t.Fatalf("after second restart %d messages, want 14", len(readOnly))
	}
	rejected := do(t, thirdHandler, http.MethodPost, path, "teacher", `{"content":"Поздний ответ."}`)
	if rejected.Code != http.StatusConflict {
		t.Fatalf("POST on finished run = %d, want 409: %s", rejected.Code, rejected.Body.String())
	}
	if code := decodeErrorCode(t, rejected.Body.Bytes()); code != "SESSION_FINISHED" {
		t.Fatalf("error code = %q, want SESSION_FINISHED", code)
	}
	var afterReject int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM run_chat_messages WHERE run_id=$1`, runID).Scan(&afterReject); err != nil {
		t.Fatal(err)
	}
	if afterReject != 14 {
		t.Fatalf("finished run accepted a message: %d rows", afterReject)
	}
}

// gatedPersistentInterviewer answers the first question immediately and blocks
// every later call until release, making a concurrent POST race deterministic.
type gatedPersistentInterviewer struct {
	calls   atomic.Int32
	release chan struct{}
}

func (g *gatedPersistentInterviewer) Chat(_ context.Context, _ []llm.Message) (string, error) {
	if g.calls.Add(1) > 1 {
		<-g.release
	}
	return `{"question":{"id":"q","level":"role","text":"Расскажите о вашей роли."},"rationale":"Старт интервью.","is_clarification":false}`, nil
}

// TestChatSessionConcurrentPostIsSerialized verifies that two simultaneous
// answers cannot advance the durable interview twice.
func TestChatSessionConcurrentPostIsSerialized(t *testing.T) {
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая БД (INGEST_TEST_DATABASE_URL)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	requireChatSessionSchema(t, pool)

	pg := NewPGStore(pool)
	runID := createDurableChatRun(t, pg, pool, "Аналитик данных")
	path := "/api/v1/runs/" + runID + "/messages"

	client := &gatedPersistentInterviewer{release: make(chan struct{})}
	service := chat.NewService(client, func(_ string, status domain.RunStatus) {
		_ = pg.SetStatus(runID, status)
	})
	service.SetSessionStore(store.NewChatSessionStore(pool))
	handler := NewServerWithChat(pg, service).Routes()
	if err := service.Start(runID, "Аналитик данных"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForChatMessages(t, handler, runID, 2)

	const posters = 6
	codes := make(chan int, posters)
	var wg sync.WaitGroup
	for index := 0; index < posters; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := do(t, handler, http.MethodPost, path, "teacher", `{"content":"Конкретный ответ про аналитику данных."}`)
			codes <- rec.Code
		}()
	}
	wg.Wait()
	close(codes)
	created, conflicts := 0, 0
	for code := range codes {
		switch code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("unexpected POST status %d", code)
		}
	}
	if created != 1 {
		t.Fatalf("accepted %d concurrent answers, want exactly 1", created)
	}
	if conflicts != posters-1 {
		t.Fatalf("conflicts = %d, want %d", conflicts, posters-1)
	}

	// Release the model gate: the next question arrives and the stored dialog
	// contains exactly one user answer.
	close(client.release)
	waitForChatMessages(t, handler, runID, 4)
	var userRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM run_chat_messages WHERE run_id=$1 AND role='user'`, runID).Scan(&userRows); err != nil {
		t.Fatal(err)
	}
	if userRows != 1 {
		t.Fatalf("stored %d user messages, want exactly 1", userRows)
	}
}

// TestChatSessionStoreOptimisticVersion covers the adapter contract directly:
// a stale writer is rejected instead of overwriting a newer session step.
func TestChatSessionStoreOptimisticVersion(t *testing.T) {
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая БД (INGEST_TEST_DATABASE_URL)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	requireChatSessionSchema(t, pool)

	pg := NewPGStore(pool)
	runID := createDurableChatRun(t, pg, pool, "Аналитик данных")
	service := newSessionChatService(pool, nil)
	if err := service.Start(runID, "Аналитик данных"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Wait until the greeting and the first question were persisted atomically.
	adapter := store.NewChatSessionStore(pool)
	deadline := time.Now().Add(10 * time.Second)
	var snapshot chat.SessionSnapshot
	for {
		snapshot, err = adapter.LoadSession(ctx, runID)
		if err == nil && len(snapshot.Interviewer.State.Questions) >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("chat session was not persisted: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if snapshot.Version < 1 {
		t.Fatalf("snapshot version = %d, want >= 1", snapshot.Version)
	}
	if err := adapter.SaveStep(ctx, runID, snapshot, nil); err != nil {
		t.Fatalf("first SaveStep: %v", err)
	}
	// The same expected version is stale now: another writer already advanced it.
	if err := adapter.SaveStep(ctx, runID, snapshot, nil); !errors.Is(err, chat.ErrVersionConflict) {
		t.Fatalf("stale SaveStep = %v, want ErrVersionConflict", err)
	}

	// Two concurrent writers with the same base version: exactly one wins.
	current, err := adapter.LoadSession(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var conflicts atomic.Int32
	var wg sync.WaitGroup
	for index := 0; index < 2; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := adapter.SaveStep(ctx, runID, current, nil)
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, chat.ErrVersionConflict):
				conflicts.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || conflicts.Load() != 1 {
		t.Fatalf("concurrent SaveStep: wins=%d conflicts=%d, want 1/1", wins.Load(), conflicts.Load())
	}
}

// waitForChatSessionStatus polls run_chat_sessions.status until it reaches want.
func waitForChatSessionStatus(t *testing.T, pool *pgxpool.Pool, runID string, want chat.SessionStatus) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(), `SELECT status FROM run_chat_sessions WHERE run_id=$1`, runID).Scan(&last); err == nil && last == string(want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("chat session %s status = %q, want %q", runID, last, want)
}

// TestChatSessionResumesInterruptedFinalization covers a restart after the last
// answer was stored: the post-interview pipeline must be resumed and the final
// Stepik message written, so the run cannot stay "running" forever.
func TestChatSessionResumesInterruptedFinalization(t *testing.T) {
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая БД (INGEST_TEST_DATABASE_URL)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	requireChatSessionSchema(t, pool)

	pg := NewPGStore(pool)
	runID := createDurableChatRun(t, pg, pool, "Аналитик данных")
	path := "/api/v1/runs/" + runID + "/messages"

	// The first process collects every answer, then its finalizer blocks forever
	// (simulating a process that died in the middle of the pipeline).
	blockFinalizer := make(chan struct{})
	first := chat.NewService(persistentInterviewer{}, nil)
	first.SetSessionStore(store.NewChatSessionStore(pool))
	first.SetFinalizer(func(context.Context, string, string, domain.InterviewSession) (domain.RunStatus, error) {
		<-blockFinalizer
		return domain.RunDone, nil
	})
	firstHandler := NewServerWithChat(pg, first).Routes()
	if err := first.Start(runID, "Аналитик данных"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for answerIndex := 0; answerIndex < 6; answerIndex++ {
		waitForChatMessages(t, firstHandler, runID, 2*answerIndex+2)
		rec := do(t, firstHandler, http.MethodPost, path, "teacher", `{"content":"Конкретный ответ про аналитику данных."}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST %d = %d: %s", answerIndex, rec.Code, rec.Body.String())
		}
	}
	waitForChatSessionStatus(t, pool, runID, chat.SessionFinalizing)
	t.Cleanup(func() { close(blockFinalizer) })

	// Restart: recovery sees the finalizing session and finishes the pipeline.
	finalized := make(chan struct{}, 1)
	restarted := newSessionChatService(pool, func(_ string, status domain.RunStatus) {
		_ = pg.SetStatus(runID, status)
	})
	restarted.SetFinalizer(func(context.Context, string, string, domain.InterviewSession) (domain.RunStatus, error) {
		finalized <- struct{}{}
		return domain.RunDone, nil
	})
	restarted.RecoverSessions(ctx)
	select {
	case <-finalized:
	case <-time.After(15 * time.Second):
		t.Fatal("finalizer was not resumed after restart")
	}
	restartedHandler := NewServerWithChat(pg, restarted).Routes()
	final := waitForChatMessages(t, restartedHandler, runID, 14)
	last := final[len(final)-1]
	if last.Role != "agent" || !strings.Contains(last.Content, "«Дерево»") {
		t.Fatalf("final Stepik message was not persisted after resumed finalization: %+v", last)
	}
	waitForRunStatus(t, pool, runID, domain.RunDone)
	waitForChatSessionStatus(t, pool, runID, chat.SessionCompleted)
}

// TestLegacyChatRunIsFailedHonestly covers a chat run created before durable
// session snapshots: POST returns SESSION_INTERRUPTED and the startup recovery
// marks the run failed instead of leaving it running forever.
func TestLegacyChatRunIsFailedHonestly(t *testing.T) {
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая БД (INGEST_TEST_DATABASE_URL)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	requireChatSessionSchema(t, pool)

	pg := NewPGStore(pool)
	// Both legacy runs must predate the service so the startup recovery sweep
	// considers them interrupted.
	runPost := createDurableChatRun(t, pg, pool, "Аналитик данных")
	runSweep := createDurableChatRun(t, pg, pool, "Аналитик данных")
	// Simulate the old backend: history exists, no session snapshot row.
	messageStore := store.NewChatMessageStore(pool)
	for _, runID := range []string{runPost, runSweep} {
		if err := messageStore.AppendMessage(ctx, runID, chat.Message{
			ID:        "msg-" + runID + "-1",
			Role:      "agent",
			Content:   "Здравствуйте! Я чат-интервьюер.",
			Sequence:  1,
			CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}

	service := newSessionChatService(pool, func(runID string, status domain.RunStatus) {
		_ = pg.SetStatus(runID, status)
	})
	handler := NewServerWithChat(pg, service).Routes()
	path := "/api/v1/runs/" + runPost + "/messages"

	// Continuing a legacy run is rejected explicitly and marks it failed.
	rec := do(t, handler, http.MethodPost, path, "teacher", `{"content":"Ответ."}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("legacy POST = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "SESSION_INTERRUPTED" {
		t.Fatalf("error code = %q, want SESSION_INTERRUPTED", code)
	}
	waitForRunStatus(t, pool, runPost, domain.RunFailed)

	// History stays readable after the run is marked failed.
	if code, messages := getChatMessages(t, handler, runPost, "student"); code != http.StatusOK || len(messages) != 1 {
		t.Fatalf("legacy GET = %d, %d messages; want 200 with 1 message", code, len(messages))
	}

	// The startup sweep catches legacy runs that were never touched by a client.
	service.RecoverSessions(ctx)
	waitForRunStatus(t, pool, runSweep, domain.RunFailed)
}

// TestChatSessionStoreReportsMissingMigration ensures a database without the new
// migration fails loudly instead of silently losing the interview cursor.
func TestChatSessionStoreReportsMissingMigration(t *testing.T) {
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая БД (INGEST_TEST_DATABASE_URL)")
	}
	ctx := context.Background()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	// Hide the application tables while keeping the DSN valid: this is what a
	// backend started against an un-migrated database sees.
	config.ConnConfig.RuntimeParams["search_path"] = "pg_catalog"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var ready bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('run_chat_sessions') IS NOT NULL`).Scan(&ready); err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Skip("search_path isolation did not hide the application schema")
	}
	_, err = store.NewChatSessionStore(pool).LoadSession(ctx, "00000000-0000-0000-0000-000000000001")
	if err == nil {
		t.Fatal("LoadSession succeeded without the run_chat_sessions migration")
	}
	if errors.Is(err, chat.ErrNotFound) {
		t.Fatalf("missing migration was reported as a missing run: %v", err)
	}
	if !strings.Contains(err.Error(), "run_chat_sessions") && !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("unexpected missing-migration error: %v", err)
	}
}
