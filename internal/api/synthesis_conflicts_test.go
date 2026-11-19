package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"testing"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

// synthesisReviewStub подменяет PostgreSQL-чтение review синтеза в HTTP-тестах.
type synthesisReviewStub struct {
	Store
	runID     string
	conflicts []domain.Conflict
	messages  []agent.AgentMessage
	err       error
}

func (s *synthesisReviewStub) listSynthesisConflicts(_ context.Context, runID string) ([]domain.Conflict, error) {
	s.runID = runID
	return s.conflicts, s.err
}

func (s *synthesisReviewStub) listSynthesisMessages(_ context.Context, runID string) ([]agent.AgentMessage, error) {
	s.runID = runID
	return s.messages, s.err
}

func sampleConflict(id, nodeID string) domain.Conflict {
	return domain.Conflict{
		ID:     id,
		NodeID: nodeID,
		Field:  "title",
		Values: []domain.ConflictValue{
			{SourceID: "src-interview", SourceType: domain.SourceInterview, Value: "Аналитик", Priority: domain.PriorityInterview},
			{SourceID: "src-vacancy", SourceType: domain.SourceVacancy, Value: "Аналитик данных", Priority: domain.PriorityVacancy},
		},
		Winner: domain.ConflictValue{SourceID: "src-interview", SourceType: domain.SourceInterview, Value: "Аналитик", Priority: domain.PriorityInterview},
		Reason: "higher source priority wins: interview (300)",
	}
}

func TestSynthesisReviewEndpointsAuth(t *testing.T) {
	stub := &synthesisReviewStub{conflicts: []domain.Conflict{}, messages: []agent.AgentMessage{}}
	handler := NewServer(stub).Routes()
	tests := []struct {
		role string
		want int
	}{
		{"admin", http.StatusOK},
		{"teacher", http.StatusOK},
		{"rop", http.StatusOK},
		{"student", http.StatusForbidden},
		{"guest", http.StatusForbidden},
		{"", http.StatusUnauthorized},
	}
	for _, test := range tests {
		for _, path := range []string{"/api/v1/runs/run-1/conflicts", "/api/v1/runs/run-1/agent-messages"} {
			t.Run("role="+test.role+" path="+path, func(t *testing.T) {
				want := test.want
				if test.role == "student" && path == "/api/v1/runs/run-1/conflicts" {
					want = http.StatusOK
				}
				rec := do(t, handler, http.MethodGet, path, test.role, "")
				if rec.Code != want {
					t.Fatalf("роль %q: ожидали %d, получили %d (%s)", test.role, want, rec.Code, rec.Body.String())
				}
			})
		}
	}
}

func TestRunConflictsHandlerSuccess(t *testing.T) {
	want := []domain.Conflict{sampleConflict("c-1", "tax-task-sql"), sampleConflict("c-2", "tax-fn-reporting")}
	stub := &synthesisReviewStub{conflicts: want}
	handler := NewServer(stub).Routes()

	rec := do(t, handler, http.MethodGet, "/api/v1/runs/run-42/conflicts", "teacher", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", rec.Code, rec.Body.String())
	}
	if stub.runID != "run-42" {
		t.Fatalf("в хранилище передан runID %q, ожидали run-42", stub.runID)
	}
	var got []domain.Conflict
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("ответ не разобрался как массив конфликтов: %v (%s)", err, rec.Body.String())
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("конфликты = %+v, ожидали %+v", got, want)
	}
}

func TestRunConflictsHandlerEmptyIsArray(t *testing.T) {
	stub := &synthesisReviewStub{}
	handler := NewServer(stub).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/runs/run-1/conflicts", "admin", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "[]\n" {
		t.Fatalf("пустой список конфликтов должен быть [], получили %q", body)
	}
}

func TestRunAgentMessagesHandlerSuccess(t *testing.T) {
	conflict := sampleConflict("c-1", "tax-task-sql")
	payload, err := json.Marshal(conflict)
	if err != nil {
		t.Fatal(err)
	}
	want := []agent.AgentMessage{{ID: "msg-conflict-1", Kind: "conflict", Text: "Конфликт title", Payload: payload}}
	stub := &synthesisReviewStub{messages: want}
	handler := NewServer(stub).Routes()

	rec := do(t, handler, http.MethodGet, "/api/v1/runs/run-7/agent-messages", "rop", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", rec.Code, rec.Body.String())
	}
	if stub.runID != "run-7" {
		t.Fatalf("в хранилище передан runID %q, ожидали run-7", stub.runID)
	}
	var got []agent.AgentMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("ответ не разобрался как массив сообщений: %v (%s)", err, rec.Body.String())
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("сообщения = %+v, ожидали %+v", got, want)
	}
	var decoded domain.Conflict
	if err := json.Unmarshal(got[0].Payload, &decoded); err != nil || decoded.ID != conflict.ID {
		t.Fatalf("payload сообщения не содержит конфликт: %v, %+v", err, decoded)
	}
}

func TestSynthesisReviewHandlerNotFound(t *testing.T) {
	stub := &synthesisReviewStub{err: ErrNotFound}
	handler := NewServer(stub).Routes()
	for _, path := range []string{"/api/v1/runs/missing/conflicts", "/api/v1/runs/missing/agent-messages"} {
		rec := do(t, handler, http.MethodGet, path, "teacher", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: ожидали 404, получили %d (%s)", path, rec.Code, rec.Body.String())
		}
	}
}

func TestSynthesisReviewHandlerStoreError(t *testing.T) {
	stub := &synthesisReviewStub{err: errors.New("boom")}
	handler := NewServer(stub).Routes()
	for _, path := range []string{"/api/v1/runs/run-1/conflicts", "/api/v1/runs/run-1/agent-messages"} {
		rec := do(t, handler, http.MethodGet, path, "teacher", "")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s: ожидали 500, получили %d (%s)", path, rec.Code, rec.Body.String())
		}
	}
}

func TestSynthesisReviewHandlerNotImplemented(t *testing.T) {
	handler := NewServer(newMemStore()).Routes()
	for _, path := range []string{"/api/v1/runs/run-1/conflicts", "/api/v1/runs/run-1/agent-messages"} {
		rec := do(t, handler, http.MethodGet, path, "teacher", "")
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("%s: для in-memory хранилища ожидали 501, получили %d (%s)", path, rec.Code, rec.Body.String())
		}
	}
}

// ---- PostgreSQL integration ----

func synthesisReviewTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая БД (INGEST_TEST_DATABASE_URL)")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestSynthesisReviewPostgresRoundTrip(t *testing.T) {
	pool := synthesisReviewTestPool(t)
	ctx := context.Background()
	p := NewPGStore(pool)
	handler := NewServer(p).Routes()

	run, err := p.CreateRun("Проверка конфликтов синтеза")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM runs WHERE id=$1`, run.ID)
	})

	conflict := sampleConflict("c-1", "tax-task-sql")
	payload, err := json.Marshal(conflict)
	if err != nil {
		t.Fatal(err)
	}
	messages := []agent.AgentMessage{{ID: "msg-conflict-1", Kind: "conflict", Text: "Конфликт title", Payload: payload}}
	if err = p.SaveSynthesisReview(ctx, run.ID, []domain.Conflict{conflict}, messages); err != nil {
		t.Fatalf("SaveSynthesisReview: %v", err)
	}

	// Запись реально лежит в agent_messages с run_id — отдельная миграция не нужна.
	var stored int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM agent_messages WHERE run_id=$1::uuid AND message_type IN ('synthesis.conflict','synthesis.message')`, run.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 2 {
		t.Fatalf("в agent_messages записано %d строк синтеза, ожидали 2", stored)
	}

	rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID+"/conflicts", "teacher", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("конфликты: %d %s", rec.Code, rec.Body.String())
	}
	var gotConflicts []domain.Conflict
	if err = json.Unmarshal(rec.Body.Bytes(), &gotConflicts); err != nil {
		t.Fatalf("конфликты не разобрались: %v (%s)", err, rec.Body.String())
	}
	if !reflect.DeepEqual(gotConflicts, []domain.Conflict{conflict}) {
		t.Fatalf("конфликты = %+v, ожидали %+v", gotConflicts, []domain.Conflict{conflict})
	}

	rec = do(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID+"/agent-messages", "teacher", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("сообщения: %d %s", rec.Code, rec.Body.String())
	}
	var gotMessages []agent.AgentMessage
	if err = json.Unmarshal(rec.Body.Bytes(), &gotMessages); err != nil {
		t.Fatalf("сообщения не разобрались: %v (%s)", err, rec.Body.String())
	}
	if len(gotMessages) != len(messages) {
		t.Fatalf("сообщения = %+v, ожидали %+v", gotMessages, messages)
	}
	for index := range messages {
		got, want := gotMessages[index], messages[index]
		if got.ID != want.ID || got.Kind != want.Kind || got.Text != want.Text {
			t.Fatalf("сообщение = %+v, ожидали %+v", got, want)
		}
		// jsonb в PostgreSQL переставляет ключи объекта, поэтому payload
		// сравниваем после разбора, а не как байты.
		var gotConflict, wantConflict domain.Conflict
		if err = json.Unmarshal(got.Payload, &gotConflict); err != nil {
			t.Fatalf("payload сообщения не конфликт: %v", err)
		}
		if err = json.Unmarshal(want.Payload, &wantConflict); err != nil {
			t.Fatalf("payload эталона не конфликт: %v", err)
		}
		if !reflect.DeepEqual(gotConflict, wantConflict) {
			t.Fatalf("payload сообщения = %+v, ожидали %+v", gotConflict, wantConflict)
		}
	}

	// Повторное сохранение заменяет прежние элементы, а не дублирует их.
	if err = p.SaveSynthesisReview(ctx, run.ID, nil, nil); err != nil {
		t.Fatalf("повторный SaveSynthesisReview: %v", err)
	}
	rec = do(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID+"/conflicts", "teacher", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Fatalf("после замены конфликтов ожидали [], получили %d %q", rec.Code, rec.Body.String())
	}

	// Невалидный и неизвестный run дают 404.
	for _, bad := range []string{"not-a-uuid", "00000000-0000-4000-8000-000000000000"} {
		rec = do(t, handler, http.MethodGet, "/api/v1/runs/"+bad+"/conflicts", "teacher", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("run %q: ожидали 404, получили %d (%s)", bad, rec.Code, rec.Body.String())
		}
	}

	// SaveSynthesisReview идемпотентен: чужой/несуществующий run — ErrNotFound.
	if err = p.SaveSynthesisReview(ctx, "not-a-uuid", nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SaveSynthesisReview невалидного run: %v, ожидали ErrNotFound", err)
	}
}
