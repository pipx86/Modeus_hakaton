package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"testing"

	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

// reviewQueueStub — заглушка reviewQueueReader для проверки HTTP-слоя без БД.
type reviewQueueStub struct {
	Store
	calls  int
	status domain.EvidenceStatus
	limit  int
	offset int
	items  []disputedEvidenceItem
	err    error
}

func (s *reviewQueueStub) listReviewQueue(_ context.Context, status domain.EvidenceStatus, limit, offset int) ([]disputedEvidenceItem, error) {
	s.calls++
	s.status, s.limit, s.offset = status, limit, offset
	return s.items, s.err
}

func TestParseReviewQueueQuery(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantStatus domain.EvidenceStatus
		wantLimit  int
		wantOffset int
		wantErr    bool
	}{
		{name: "defaults", query: "", wantStatus: domain.EvidencePending, wantLimit: reviewQueueDefaultLimit, wantOffset: 0},
		{name: "pending", query: "status=pending", wantStatus: domain.EvidencePending, wantLimit: 50, wantOffset: 0},
		{name: "rejected", query: "status=rejected", wantStatus: domain.EvidenceRejected, wantLimit: 50, wantOffset: 0},
		{name: "trimmed", query: "status=%20rejected%20&limit=%2010%20&offset=%205%20", wantStatus: domain.EvidenceRejected, wantLimit: 10, wantOffset: 5},
		{name: "limit min", query: "limit=1", wantStatus: domain.EvidencePending, wantLimit: 1, wantOffset: 0},
		{name: "limit max", query: "limit=200", wantStatus: domain.EvidencePending, wantLimit: 200, wantOffset: 0},
		{name: "offset", query: "offset=42", wantStatus: domain.EvidencePending, wantLimit: 50, wantOffset: 42},
		{name: "approved is not in queue", query: "status=approved", wantErr: true},
		{name: "unknown status", query: "status=maybe", wantErr: true},
		{name: "limit zero", query: "limit=0", wantErr: true},
		{name: "limit too big", query: "limit=201", wantErr: true},
		{name: "limit not number", query: "limit=ten", wantErr: true},
		{name: "offset negative", query: "offset=-1", wantErr: true},
		{name: "offset not number", query: "offset=x", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, err := url.ParseQuery(test.query)
			if err != nil {
				t.Fatal(err)
			}
			status, limit, offset, err := parseReviewQueueQuery(values)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ожидали ошибку для %q", test.query)
				}
				return
			}
			if err != nil {
				t.Fatalf("неожидали ошибку: %v", err)
			}
			if status != test.wantStatus || limit != test.wantLimit || offset != test.wantOffset {
				t.Fatalf("получили status=%q limit=%d offset=%d, ожидали %q/%d/%d",
					status, limit, offset, test.wantStatus, test.wantLimit, test.wantOffset)
			}
		})
	}
}

func TestReviewQueueHandlerAuth(t *testing.T) {
	stub := &reviewQueueStub{items: []disputedEvidenceItem{}}
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
		t.Run("role="+test.role, func(t *testing.T) {
			rec := do(t, handler, http.MethodGet, "/api/v1/reviews/evidence", test.role, "")
			if rec.Code != test.want {
				t.Fatalf("роль %q: ожидали %d, получили %d (%s)", test.role, test.want, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestReviewQueueHandlerDefaults(t *testing.T) {
	stub := &reviewQueueStub{}
	handler := NewServer(stub).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/reviews/evidence", "teacher", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", rec.Code, rec.Body.String())
	}
	if stub.calls != 1 || stub.status != domain.EvidencePending || stub.limit != reviewQueueDefaultLimit || stub.offset != 0 {
		t.Fatalf("неверные параметры по умолчанию: calls=%d status=%q limit=%d offset=%d",
			stub.calls, stub.status, stub.limit, stub.offset)
	}
	if body := rec.Body.String(); body != "[]\n" {
		t.Fatalf("пустая очередь должна отдавать JSON-массив []: %q", body)
	}
}

func TestReviewQueueHandlerFilters(t *testing.T) {
	stub := &reviewQueueStub{}
	handler := NewServer(stub).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/reviews/evidence?status=rejected&limit=10&offset=20", "teacher", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", rec.Code, rec.Body.String())
	}
	if stub.status != domain.EvidenceRejected || stub.limit != 10 || stub.offset != 20 {
		t.Fatalf("фильтр не передан: status=%q limit=%d offset=%d", stub.status, stub.limit, stub.offset)
	}
}

func TestReviewQueueHandlerValidation(t *testing.T) {
	tests := []string{
		"?status=approved",
		"?status=unknown",
		"?limit=0",
		"?limit=201",
		"?limit=ten",
		"?offset=-1",
		"?offset=ten",
	}
	for _, query := range tests {
		t.Run(query, func(t *testing.T) {
			stub := &reviewQueueStub{}
			handler := NewServer(stub).Routes()
			rec := do(t, handler, http.MethodGet, "/api/v1/reviews/evidence"+query, "teacher", "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("ожидали 400 для %q, получили %d (%s)", query, rec.Code, rec.Body.String())
			}
			if stub.calls != 0 {
				t.Fatalf("хранилище не должно вызываться при невалидных параметрах (calls=%d)", stub.calls)
			}
		})
	}
}

func TestReviewQueueHandlerValidationBeforeStorage(t *testing.T) {
	// In-memory store не умеет очередь, но невалидный статус всё равно 400:
	// параметры проверяются до выбора бэкенда.
	handler := NewServer(newMemStore()).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/reviews/evidence?status=approved", "admin", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ожидали 400 на невалидный статус при in-memory store, получили %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestReviewQueueHandlerNotImplemented(t *testing.T) {
	handler := NewServer(newMemStore()).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/reviews/evidence", "admin", "")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("in-memory store: ожидали 501, получили %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestReviewQueueHandlerStoreError(t *testing.T) {
	stub := &reviewQueueStub{err: errors.New("boom")}
	handler := NewServer(stub).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/reviews/evidence", "admin", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("ожидали 500, получили %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestReviewQueueHandlerSuccess(t *testing.T) {
	stub := &reviewQueueStub{items: []disputedEvidenceItem{
		{ID: "ev-1", RunID: "run-1", NodeID: "node-1", NodeTitle: "Роль", SourceID: "src-1",
			SourceTitle: "Источник", SourceURL: "https://example.org/source", RefID: "msg-1", Quote: "цитата", Status: domain.EvidencePending},
	}}
	handler := NewServer(stub).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/reviews/evidence?limit=1", "teacher", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", rec.Code, rec.Body.String())
	}
	var items []disputedEvidenceItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("ответ не разобрался как массив: %v (%s)", err, rec.Body.String())
	}
	want := []disputedEvidenceItem{{ID: "ev-1", RunID: "run-1", NodeID: "node-1", NodeTitle: "Роль", SourceID: "src-1",
		SourceTitle: "Источник", SourceURL: "https://example.org/source", RefID: "msg-1", Quote: "цитата", Status: domain.EvidencePending}}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("строка очереди: %+v, ожидали %+v", items, want)
	}
}

// --- PostgreSQL integration ---

func reviewQueueTestPool(t *testing.T) *pgxpool.Pool {
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

func reviewQueueTestUUID(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// reviewQueueItemsOfRun фильтрует глобальную очередь по run: очередь общая для
// всех исследований, поэтому проверки не должны зависеть от чужих записей.
func reviewQueueItemsOfRun(items []disputedEvidenceItem, runID string) []disputedEvidenceItem {
	result := make([]disputedEvidenceItem, 0, len(items))
	for _, item := range items {
		if item.RunID == runID {
			result = append(result, item)
		}
	}
	return result
}

func TestReviewQueueЧерезAPI(t *testing.T) {
	pool := reviewQueueTestPool(t)
	ctx := context.Background()
	p := NewPGStore(pool)
	handler := NewServer(p).Routes()

	source, err := p.CreateSource(domain.Source{Type: domain.SourceInterview, Title: "Очередь: источник",
		Content: "Текст интервью", SourceURL: "https://example.org/interview"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.CreateRun("Очередь проверки")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM runs WHERE id=$1`, run.ID)
		pool.Exec(context.Background(), `DELETE FROM sources WHERE id=$1`, source.ID)
	})

	rootID := reviewQueueTestUUID(t, pool)
	childID := reviewQueueTestUUID(t, pool)
	pendingRootID := reviewQueueTestUUID(t, pool)
	rejectedID := reviewQueueTestUUID(t, pool)
	approvedID := reviewQueueTestUUID(t, pool)
	pendingChildID := reviewQueueTestUUID(t, pool)
	nodes := []domain.Node{
		{ID: rootID, Type: domain.NodeRole, Title: "Роль", Evidence: []domain.Evidence{
			{ID: pendingRootID, SourceID: source.ID, RefID: "msg-1", Quote: "Ожидает проверки"},
			{ID: rejectedID, SourceID: source.ID, RefID: "msg-2", Quote: "Будет отклонено"},
			{ID: approvedID, SourceID: source.ID, RefID: "msg-3", Quote: "Будет подтверждено"},
		}},
		{ID: childID, Type: domain.NodeFunction, Title: "Функция", ParentID: &rootID, Evidence: []domain.Evidence{
			{ID: pendingChildID, SourceID: source.ID, RefID: "msg-4", Quote: "Ожидает проверки в дочернем узле"},
		}},
	}
	if err = p.SaveTree(ctx, run.ID, nodes); err != nil {
		t.Fatal(err)
	}
	if err = p.RejectEvidence(rejectedID); err != nil {
		t.Fatal(err)
	}
	if err = p.ApproveEvidence(approvedID); err != nil {
		t.Fatal(err)
	}
	extra, err := p.createEvidence(ctx, rootID, domain.Evidence{SourceID: source.ID, RefID: "msg-5", Quote: "Добавлено экспертом"})
	if err != nil {
		t.Fatal(err)
	}

	// По умолчанию — pending: approved в очередь не попадает.
	rec := do(t, handler, http.MethodGet, "/api/v1/reviews/evidence", "teacher", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("очередь: %d %s", rec.Code, rec.Body.String())
	}
	var pending []disputedEvidenceItem
	if err = json.Unmarshal(rec.Body.Bytes(), &pending); err != nil {
		t.Fatalf("очередь не разобралась: %v (%s)", err, rec.Body.String())
	}
	ownPending := reviewQueueItemsOfRun(pending, run.ID)
	if len(ownPending) != 3 {
		t.Fatalf("ожидали 3 pending подтверждения своего run, получили %d: %+v", len(ownPending), ownPending)
	}
	seen := map[string]disputedEvidenceItem{}
	for _, item := range ownPending {
		if item.Status != domain.EvidencePending {
			t.Fatalf("в очереди pending оказался статус %q: %+v", item.Status, item)
		}
		seen[item.ID] = item
	}
	if _, ok := seen[approvedID]; ok {
		t.Fatal("approved-подтверждение не должно попадать в очередь")
	}
	rootItem, ok := seen[pendingRootID]
	if !ok {
		t.Fatalf("в очереди нет %s: %+v", pendingRootID, ownPending)
	}
	if childItem := seen[pendingChildID]; childItem.NodeID != childID || childItem.NodeTitle != "Функция" || childItem.RefID != "msg-4" || childItem.Quote != "Ожидает проверки в дочернем узле" {
		t.Fatalf("дочерняя строка очереди: %+v", childItem)
	}
	if rootItem.NodeID != rootID || rootItem.NodeTitle != "Роль" || rootItem.RunID != run.ID ||
		rootItem.SourceID != source.ID || rootItem.SourceTitle != "Очередь: источник" ||
		rootItem.SourceURL != "https://example.org/interview" || rootItem.RefID != "msg-1" || rootItem.Quote != "Ожидает проверки" {
		t.Fatalf("контекст строки очереди: %+v", rootItem)
	}
	if extraItem, ok := seen[extra.ID]; !ok || extraItem.Quote != "Добавлено экспертом" {
		t.Fatalf("добавленное подтверждение отсутствует в очереди: %+v", extraItem)
	}

	// status=rejected отдаёт только отклонённые.
	rec = do(t, handler, http.MethodGet, "/api/v1/reviews/evidence?status=rejected", "teacher", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("rejected-очередь: %d %s", rec.Code, rec.Body.String())
	}
	var rejected []disputedEvidenceItem
	if err = json.Unmarshal(rec.Body.Bytes(), &rejected); err != nil {
		t.Fatal(err)
	}
	ownRejected := reviewQueueItemsOfRun(rejected, run.ID)
	if len(ownRejected) != 1 || ownRejected[0].ID != rejectedID || ownRejected[0].Status != domain.EvidenceRejected {
		t.Fatalf("rejected-очередь: %+v", ownRejected)
	}

	// Пагинация: обходим страницы limit=2, каждое своё pending-подтверждение
	// должно встретиться ровно один раз.
	collected := map[string]int{}
	offset := 0
	const maxPages = 100
	for page := 0; page < maxPages; page++ {
		path := "/api/v1/reviews/evidence?limit=2&offset=" + strconv.Itoa(offset)
		rec = do(t, handler, http.MethodGet, path, "teacher", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("страница offset=%d: %d %s", offset, rec.Code, rec.Body.String())
		}
		var items []disputedEvidenceItem
		if err = json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
			t.Fatal(err)
		}
		if len(items) > 2 {
			t.Fatalf("limit=2 вернул %d строк", len(items))
		}
		if len(items) == 0 {
			break
		}
		for _, item := range reviewQueueItemsOfRun(items, run.ID) {
			collected[item.ID]++
		}
		offset += len(items)
		if offset > 10000 {
			t.Fatalf("пагинация не завершилась за %d страниц", maxPages)
		}
	}
	for _, id := range []string{pendingRootID, pendingChildID, extra.ID} {
		if collected[id] != 1 {
			t.Fatalf("pending-подтверждение %s встретилось %d раз на страницах (ожидали 1)", id, collected[id])
		}
	}
	if collected[rejectedID] != 0 || collected[approvedID] != 0 {
		t.Fatalf("в pending-пагинацию попали лишние записи: %+v", collected)
	}

	// Валидация параметров и прав.
	for _, query := range []string{"?status=approved", "?limit=0", "?limit=201", "?limit=x", "?offset=-1", "?offset=x"} {
		if rec = do(t, handler, http.MethodGet, "/api/v1/reviews/evidence"+query, "teacher", ""); rec.Code != http.StatusBadRequest {
			t.Fatalf("%q: ожидали 400, получили %d (%s)", query, rec.Code, rec.Body.String())
		}
	}
	if rec = do(t, handler, http.MethodGet, "/api/v1/reviews/evidence", "student", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("student: %d", rec.Code)
	}
	if rec = do(t, handler, http.MethodGet, "/api/v1/reviews/evidence", "guest", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("guest: %d", rec.Code)
	}
	if rec = do(t, handler, http.MethodGet, "/api/v1/reviews/evidence", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("без токена: %d", rec.Code)
	}
}
