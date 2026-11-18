package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

// doMentorHandler вызывает хендлер напрямую, без роутера и auth-middleware:
// ставит роль в контекст и path-параметры (ServeMux делает это в проде).
// Так тесты не зависят от того, когда Lead подключит маршруты в Routes().
func doMentorHandler(t *testing.T, handler http.HandlerFunc, method, target, role, body string, pathValues map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if role != "" {
		request = request.WithContext(auth.WithRole(request.Context(), auth.Role(role)))
	}
	for key, value := range pathValues {
		request.SetPathValue(key, value)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

// decodeMentorJSON разбирает JSON-ответ в T и падает с понятным сообщением.
func decodeMentorJSON[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		t.Fatalf("ответ не разобрался как JSON: %v (%s)", err, recorder.Body.String())
	}
	return value
}

// mustParseQuery парсит query-строку для юнит-тестов валидаторов.
func mustParseQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	values, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("query %q не разобрался: %v", raw, err)
	}
	return values
}

// mentorFeatureTestPool открывает БД для integration-тестов. Как и остальные
// интеграционные тесты репозитория, без INGEST_TEST_DATABASE_URL тест
// пропускается: dev-база не должна получать тестовые строки неожиданно.
func mentorFeatureTestPool(t *testing.T) *pgxpool.Pool {
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

// mentorFeatureTestUUID возвращает UUID, сгенерированный PostgreSQL.
func mentorFeatureTestUUID(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// --- review progress (#7) ---

type reviewProgressStub struct {
	Store
	calls  int
	runID  string
	counts reviewProgressCounts
	err    error
}

func (s *reviewProgressStub) evidenceReviewProgress(_ context.Context, runID string) (reviewProgressCounts, error) {
	s.calls++
	s.runID = runID
	return s.counts, s.err
}

// reviewProgressUnsupportedStore не умеет считать прогресс и проверяет 501.
type reviewProgressUnsupportedStore struct{ Store }

func TestParseReviewProgressQuery(t *testing.T) {
	const runUUID = "11111111-1111-1111-1111-111111111111"
	tests := []struct {
		name    string
		query   string
		want    string
		wantErr bool
	}{
		{name: "global by default", query: "", want: ""},
		{name: "run uuid", query: "run_id=" + runUUID, want: runUUID},
		{name: "trimmed", query: "run_id=%20" + runUUID + "%20", want: runUUID},
		{name: "bad uuid", query: "run_id=run-1", wantErr: true},
		{name: "truncated uuid", query: "run_id=1111", wantErr: true},
		{name: "blank treated as global", query: "run_id=%20%20", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseReviewProgressQuery(mustParseQuery(t, test.query))
			if test.wantErr {
				if err == nil {
					t.Fatalf("ожидали ошибку для %q", test.query)
				}
				return
			}
			if err != nil {
				t.Fatalf("неожидали ошибку: %v", err)
			}
			if got != test.want {
				t.Fatalf("run_id = %q, ожидали %q", got, test.want)
			}
		})
	}
}

func TestReviewProgressHandlerAuth(t *testing.T) {
	stub := &reviewProgressStub{counts: reviewProgressCounts{Pending: 1, Total: 1}}
	server := NewServer(stub)
	tests := []struct {
		role string
		want int
	}{
		{"admin", http.StatusOK},
		{"teacher", http.StatusOK},
		{"rop", http.StatusOK},
		{"student", http.StatusForbidden},
		{"guest", http.StatusForbidden},
		{"", http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run("role="+test.role, func(t *testing.T) {
			recorder := doMentorHandler(t, server.handleReviewProgress, http.MethodGet, "/api/v1/reviews/evidence/progress", test.role, "", nil)
			if recorder.Code != test.want {
				t.Fatalf("роль %q: ожидали %d, получили %d (%s)", test.role, test.want, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestReviewProgressHandlerGlobal(t *testing.T) {
	stub := &reviewProgressStub{counts: reviewProgressCounts{Pending: 3, Approved: 5, Rejected: 2, Total: 10}}
	server := NewServer(stub)
	recorder := doMentorHandler(t, server.handleReviewProgress, http.MethodGet, "/api/v1/reviews/evidence/progress", "teacher", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	if stub.calls != 1 || stub.runID != "" {
		t.Fatalf("хранилище должно получить глобальный запрос: calls=%d run_id=%q", stub.calls, stub.runID)
	}
	response := decodeMentorJSON[reviewProgressResponse](t, recorder)
	want := reviewProgressResponse{Scope: "global", Pending: 3, Approved: 5, Rejected: 2, Total: 10}
	if response != want {
		t.Fatalf("счётчики: %+v, ожидали %+v", response, want)
	}
	if strings.Contains(recorder.Body.String(), "run_id") {
		t.Fatalf("глобальный ответ не должен содержать run_id: %s", recorder.Body.String())
	}
}

func TestReviewProgressHandlerRunFilter(t *testing.T) {
	const runUUID = "22222222-2222-2222-2222-222222222222"
	stub := &reviewProgressStub{counts: reviewProgressCounts{Pending: 1, Approved: 1, Total: 2}}
	server := NewServer(stub)
	recorder := doMentorHandler(t, server.handleReviewProgress, http.MethodGet, "/api/v1/reviews/evidence/progress?run_id="+runUUID, "rop", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	if stub.runID != runUUID {
		t.Fatalf("run_id не передан в хранилище: %q", stub.runID)
	}
	response := decodeMentorJSON[reviewProgressResponse](t, recorder)
	want := reviewProgressResponse{Scope: "run", RunID: runUUID, Pending: 1, Approved: 1, Total: 2}
	if response != want {
		t.Fatalf("счётчики: %+v, ожидали %+v", response, want)
	}
}

func TestReviewProgressHandlerValidation(t *testing.T) {
	for _, query := range []string{"?run_id=run-1", "?run_id=123", "?run_id=not-a-uuid"} {
		t.Run(query, func(t *testing.T) {
			stub := &reviewProgressStub{}
			server := NewServer(stub)
			recorder := doMentorHandler(t, server.handleReviewProgress, http.MethodGet, "/api/v1/reviews/evidence/progress"+query, "admin", "", nil)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("ожидали 400 для %q, получили %d (%s)", query, recorder.Code, recorder.Body.String())
			}
			if stub.calls != 0 {
				t.Fatalf("хранилище не должно вызываться при невалидном run_id")
			}
		})
	}
}

func TestReviewProgressHandlerNotFound(t *testing.T) {
	const runUUID = "33333333-3333-3333-3333-333333333333"
	stub := &reviewProgressStub{err: ErrNotFound}
	server := NewServer(stub)
	recorder := doMentorHandler(t, server.handleReviewProgress, http.MethodGet, "/api/v1/reviews/evidence/progress?run_id="+runUUID, "admin", "", nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("неизвестный run: ожидали 404, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
}

func TestReviewProgressHandlerStoreError(t *testing.T) {
	stub := &reviewProgressStub{err: errors.New("boom")}
	server := NewServer(stub)
	recorder := doMentorHandler(t, server.handleReviewProgress, http.MethodGet, "/api/v1/reviews/evidence/progress", "admin", "", nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("ошибка БД: ожидали 500, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
}

func TestReviewProgressHandlerNotImplemented(t *testing.T) {
	server := NewServer(&reviewProgressUnsupportedStore{})
	recorder := doMentorHandler(t, server.handleReviewProgress, http.MethodGet, "/api/v1/reviews/evidence/progress", "admin", "", nil)
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("хранилище без счётчиков: ожидали 501, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
}

func TestReviewProgressMemoryStore(t *testing.T) {
	const runUUID = "44444444-4444-4444-4444-444444444444"
	source := domain.Source{ID: "55555555-5555-5555-5555-555555555555", Type: domain.SourceInterview, Title: "Интервью"}
	store := newMemStore()
	store.seedRun(domain.Run{ID: runUUID, Status: domain.RunPending, Profession: "Аналитик", Stage: domain.StageCreated})
	nodes := []domain.Node{{
		ID:    "66666666-6666-6666-6666-666666666666",
		Type:  domain.NodeRole,
		Title: "Роль",
		Evidence: []domain.Evidence{
			{ID: "ev-pending", SourceID: source.ID, RefID: "msg-1", Quote: "pending"},
			{ID: "ev-approved", SourceID: source.ID, RefID: "msg-2", Quote: "approved"},
			{ID: "ev-rejected", SourceID: source.ID, RefID: "msg-3", Quote: "rejected"},
			{ID: "ev-second-pending", SourceID: source.ID, RefID: "msg-4", Quote: "pending-2"},
		},
	}}
	store.seedNodes(runUUID, nodes...)
	for _, evidence := range nodes[0].Evidence {
		store.seedEvidence(evidence, runUUID)
	}
	if err := store.ApproveEvidence("ev-approved"); err != nil {
		t.Fatal(err)
	}
	if err := store.RejectEvidence("ev-rejected"); err != nil {
		t.Fatal(err)
	}

	server := NewServer(store)
	recorder := doMentorHandler(t, server.handleReviewProgress, http.MethodGet, "/api/v1/reviews/evidence/progress?run_id="+runUUID, "teacher", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("run-scoped прогресс: %d (%s)", recorder.Code, recorder.Body.String())
	}
	response := decodeMentorJSON[reviewProgressResponse](t, recorder)
	want := reviewProgressResponse{Scope: "run", RunID: runUUID, Pending: 2, Approved: 1, Rejected: 1, Total: 4}
	if response != want {
		t.Fatalf("счётчики прогона: %+v, ожидали %+v", response, want)
	}

	global := doMentorHandler(t, server.handleReviewProgress, http.MethodGet, "/api/v1/reviews/evidence/progress", "admin", "", nil)
	if global.Code != http.StatusOK {
		t.Fatalf("глобальный прогресс: %d (%s)", global.Code, global.Body.String())
	}
	globalResponse := decodeMentorJSON[reviewProgressResponse](t, global)
	if globalResponse.Scope != "global" || globalResponse.Pending != 2 || globalResponse.Approved != 1 || globalResponse.Rejected != 1 || globalResponse.Total != 4 {
		t.Fatalf("глобальные счётчики: %+v", globalResponse)
	}

	// Неизвестный, но синтаксически корректный run -> 404.
	unknown := doMentorHandler(t, server.handleReviewProgress, http.MethodGet, "/api/v1/reviews/evidence/progress?run_id=77777777-7777-7777-7777-777777777777", "admin", "", nil)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("неизвестный run: ожидали 404, получили %d (%s)", unknown.Code, unknown.Body.String())
	}
}

// TestReviewProgressЧерезPostgres проверяет SQL-агрегат на реальной БД.
func TestReviewProgressЧерезPostgres(t *testing.T) {
	pool := mentorFeatureTestPool(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	server := NewServer(store)

	source, err := store.CreateSource(domain.Source{Type: domain.SourceInterview, Title: "Прогресс проверки: интервью",
		Content: "Текст интервью", SourceURL: "https://example.org/progress"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun("Прогресс проверки")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM runs WHERE id=$1`, run.ID)
		pool.Exec(context.Background(), `DELETE FROM sources WHERE id=$1`, source.ID)
	})
	rootID := mentorFeatureTestUUID(t, pool)
	pendingID := mentorFeatureTestUUID(t, pool)
	approvedID := mentorFeatureTestUUID(t, pool)
	rejectedID := mentorFeatureTestUUID(t, pool)
	nodes := []domain.Node{{
		ID: rootID, Type: domain.NodeRole, Title: "Роль",
		Evidence: []domain.Evidence{
			{ID: pendingID, SourceID: source.ID, RefID: "answer-1", Quote: "Ожидает проверки"},
			{ID: approvedID, SourceID: source.ID, RefID: "answer-2", Quote: "Подтверждено"},
			{ID: rejectedID, SourceID: source.ID, RefID: "answer-3", Quote: "Отклонено"},
		},
	}}
	if err = store.SaveTree(ctx, run.ID, nodes); err != nil {
		t.Fatal(err)
	}
	if err = store.ApproveEvidence(approvedID); err != nil {
		t.Fatal(err)
	}
	if err = store.RejectEvidence(rejectedID); err != nil {
		t.Fatal(err)
	}

	recorder := doMentorHandler(t, server.handleReviewProgress, http.MethodGet, "/api/v1/reviews/evidence/progress?run_id="+run.ID, "teacher", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("прогресс прогона: %d (%s)", recorder.Code, recorder.Body.String())
	}
	response := decodeMentorJSON[reviewProgressResponse](t, recorder)
	want := reviewProgressResponse{Scope: "run", RunID: run.ID, Pending: 1, Approved: 1, Rejected: 1, Total: 3}
	if response != want {
		t.Fatalf("прогресс прогона: %+v, ожидали %+v", response, want)
	}

	global := doMentorHandler(t, server.handleReviewProgress, http.MethodGet, "/api/v1/reviews/evidence/progress", "teacher", "", nil)
	if global.Code != http.StatusOK {
		t.Fatalf("глобальный прогресс: %d (%s)", global.Code, global.Body.String())
	}
	globalResponse := decodeMentorJSON[reviewProgressResponse](t, global)
	if globalResponse.Total != globalResponse.Pending+globalResponse.Approved+globalResponse.Rejected {
		t.Fatalf("total != pending+approved+rejected: %+v", globalResponse)
	}
	if globalResponse.Total < 3 || globalResponse.Pending < 1 || globalResponse.Approved < 1 || globalResponse.Rejected < 1 {
		t.Fatalf("глобальные счётчики потеряли подтверждения своего прогона: %+v", globalResponse)
	}
	if globalResponse.Scope != "global" || globalResponse.RunID != "" {
		t.Fatalf("глобальный ответ должен быть без run_id: %+v", globalResponse)
	}

	unknown := doMentorHandler(t, server.handleReviewProgress, http.MethodGet, "/api/v1/reviews/evidence/progress?run_id="+mentorFeatureTestUUID(t, pool), "teacher", "", nil)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("неизвестный run: ожидали 404, получили %d (%s)", unknown.Code, unknown.Body.String())
	}
	bad := doMentorHandler(t, server.handleReviewProgress, http.MethodGet, "/api/v1/reviews/evidence/progress?run_id=run-1", "teacher", "", nil)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("невалидный run_id: ожидали 400, получили %d (%s)", bad.Code, bad.Body.String())
	}
}
