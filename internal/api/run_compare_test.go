package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	compareTestRunID     = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	compareTestAgainstID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	compareTestRandomID  = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
)

// runCompareStub — заглушка runComparer: проверяет HTTP-слой без БД.
type runCompareStub struct {
	Store
	calls     int
	runID     string
	againstID string
	result    runCompareResponse
	err       error
}

func (s *runCompareStub) compareRuns(runID, againstRunID string) (runCompareResponse, error) {
	s.calls++
	s.runID, s.againstID = runID, againstRunID
	return s.result, s.err
}

// runCompareTestHandler регистрирует только маршрут сравнения: маршрут в
// production добавляет handlers.go (это делает lead), поэтому тестируем хендлер
// «напрямую», не завися от его проводки.
func runCompareTestHandler(server *Server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/runs/{id}/compare", server.handleCompareRuns)
	return auth.Middleware(mux)
}

func runCompareTestPath(runID, againstID string) string {
	return "/api/v1/runs/" + runID + "/compare?against=" + againstID
}

// --- чистая часть сравнения ---

func compareTestNode(id, title, sourceID, quote string) domain.Node {
	return domain.Node{
		ID: id, Type: domain.NodeRole, Title: title,
		Evidence: []domain.Evidence{{ID: "ev-" + id, NodeID: id, SourceID: sourceID, RefID: "ref-" + id, Quote: quote, Status: domain.EvidencePending}},
	}
}

func TestBuildRunCompareResponseIdentical(t *testing.T) {
	base := []domain.Node{
		compareTestNode("node-a", "Аналитик", "src-1", "цитата A"),
		compareTestNode("node-b", "Отчёт", "src-1", "цитата B"),
	}
	head := []domain.Node{
		compareTestNode("node-a", "Аналитик", "src-1", "цитата A"),
		compareTestNode("node-b", "Отчёт", "src-1", "цитата B"),
	}
	result, err := buildRunCompareResponse(
		&domain.Run{ID: compareTestRunID, Profession: "Data Analyst"},
		&domain.Run{ID: compareTestAgainstID, Profession: "Data Analyst"},
		head, base)
	if err != nil {
		t.Fatalf("buildRunCompareResponse: %v", err)
	}
	if result.RunID != compareTestRunID || result.AgainstRunID != compareTestAgainstID || result.Profession != "Data Analyst" {
		t.Fatalf("run/against/profession: %+v", result)
	}
	if result.Summary != (snapshotDiffSummary{}) {
		t.Fatalf("summary одинаковых деревьев: %+v", result.Summary)
	}
	if !result.SameProfession || result.Match != (runCompareMatch{ByID: 2}) {
		t.Fatalf("same_profession/match: %+v", result)
	}
	if len(result.Added)+len(result.Removed)+len(result.Changed) != 0 {
		t.Fatalf("одинаковые деревья не должны давать diff: %+v", result)
	}
	if result.Added == nil || result.Removed == nil || result.Changed == nil {
		t.Fatalf("срезы должны быть не-nil: %+v", result)
	}
	// Story считается из тех же срезов: пустой diff — «изменений нет».
	if story, _ := buildComparisonStory(result.Added, result.Removed, result.Changed); story != comparisonStoryEmptyText {
		t.Fatalf("story пустого diff: %q", story)
	}
}

func TestBuildRunCompareResponseAddedRemovedChanged(t *testing.T) {
	parent := "node-parent"
	base := []domain.Node{
		compareTestNode("node-a", "Аналитик", "src-1", "цитата A"),
		compareTestNode("node-b", "Удалённый узел", "src-1", "цитата B"),
		compareTestNode("node-c", "Отчёт", "src-1", "цитата C"),
	}
	changedAfter := compareTestNode("node-a", "Аналитик (уточнено)", "src-2", "новая цитата")
	changedAfter.Evidence[0].Status = domain.EvidenceApproved
	added := domain.Node{
		ID: "node-d", Type: domain.NodeFunction, Title: "Новая функция", ParentID: &parent,
		Evidence: []domain.Evidence{{ID: "ev-node-d", NodeID: "node-d", SourceID: "src-3", RefID: "ref-d", Quote: "цитата D", Status: domain.EvidencePending}},
	}
	head := []domain.Node{
		changedAfter,
		compareTestNode("node-c", "Отчёт", "src-1", "цитата C"),
		added,
	}
	result, err := buildRunCompareResponse(
		&domain.Run{ID: compareTestRunID, Profession: "Data Analyst"},
		&domain.Run{ID: compareTestAgainstID, Profession: "Data Analyst"},
		head, base)
	if err != nil {
		t.Fatalf("buildRunCompareResponse: %v", err)
	}
	if result.Summary != (snapshotDiffSummary{Added: 1, Removed: 1, Changed: 1}) {
		t.Fatalf("summary: %+v", result.Summary)
	}
	if len(result.Added) != 1 || result.Added[0].ID != "node-d" {
		t.Fatalf("added = %+v", result.Added)
	}
	if len(result.Removed) != 1 || result.Removed[0].ID != "node-b" {
		t.Fatalf("removed = %+v", result.Removed)
	}
	if len(result.Changed) != 1 || result.Changed[0].ID != "node-a" {
		t.Fatalf("changed = %+v", result.Changed)
	}
	if !reflect.DeepEqual(result.Changed[0].Fields, []string{"title", "evidence"}) {
		t.Fatalf("поля изменения: %v", result.Changed[0].Fields)
	}
	if result.Changed[0].Before.Title != "Аналитик" || result.Changed[0].After.Title != "Аналитик (уточнено)" {
		t.Fatalf("before/after: %+v", result.Changed[0])
	}
	// Ссылки на источник/цитату сохраняются и в added, и в changed.
	if len(result.Added[0].Evidence) != 1 || result.Added[0].Evidence[0].SourceID != "src-3" ||
		result.Added[0].Evidence[0].RefID != "ref-d" || result.Added[0].Evidence[0].Quote != "цитата D" {
		t.Fatalf("evidence добавленного узла: %+v", result.Added[0].Evidence)
	}
	if len(result.Changed[0].After.Evidence) != 1 ||
		result.Changed[0].After.Evidence[0].Status != domain.EvidenceApproved ||
		result.Changed[0].After.Evidence[0].SourceID != "src-2" {
		t.Fatalf("evidence изменённого узла: %+v", result.Changed[0].After.Evidence)
	}
	// id-совпадения: node-a и node-c; семантических пар нет (added/removed
	// узлы с разными type + title), несопоставленных двое.
	if result.Match != (runCompareMatch{ByID: 2, ByTitle: 0, Unmatched: 2}) {
		t.Fatalf("match: %+v", result.Match)
	}
	wantHeader := "Узлов добавлено: 1, удалено: 1, изменено: 1."
	if story, _ := buildComparisonStory(result.Added, result.Removed, result.Changed); !strings.HasPrefix(story, wantHeader) {
		t.Fatalf("story (по summary): %q", story)
	}
}

func TestBuildRunCompareResponseTreeMissing(t *testing.T) {
	run := &domain.Run{ID: compareTestRunID, Profession: "Data Analyst"}
	against := &domain.Run{ID: compareTestAgainstID, Profession: "Data Analyst"}
	if _, err := buildRunCompareResponse(run, against, nil, []domain.Node{compareTestNode("node-a", "A", "src-1", "q")}); !errors.Is(err, ErrRunTreeMissing) {
		t.Fatalf("нет дерева у run: ожидали ErrRunTreeMissing, получили %v", err)
	}
	if _, err := buildRunCompareResponse(run, against, []domain.Node{compareTestNode("node-a", "A", "src-1", "q")}, nil); !errors.Is(err, ErrRunTreeMissing) {
		t.Fatalf("нет дерева у against: ожидали ErrRunTreeMissing, получили %v", err)
	}
}

func TestBuildRunCompareResponseProfessionMismatch(t *testing.T) {
	head := []domain.Node{compareTestNode("node-a", "A", "src-1", "q")}
	base := []domain.Node{compareTestNode("node-b", "B", "src-1", "q")}
	_, err := buildRunCompareResponse(
		&domain.Run{ID: compareTestRunID, Profession: "Data Analyst"},
		&domain.Run{ID: compareTestAgainstID, Profession: "DevOps Engineer"},
		head, base)
	if !errors.Is(err, ErrRunProfessionMismatch) {
		t.Fatalf("ожидали ErrRunProfessionMismatch, получили %v", err)
	}
	// Крайние пробелы не считаются другой профессией.
	if _, err = buildRunCompareResponse(
		&domain.Run{ID: compareTestRunID, Profession: " Data Analyst "},
		&domain.Run{ID: compareTestAgainstID, Profession: "Data Analyst"},
		head, base); err != nil {
		t.Fatalf("профессии, отличающиеся пробелами, должны совпадать: %v", err)
	}
	if _, err := buildRunCompareResponse(nil, &domain.Run{ID: compareTestAgainstID, Profession: "Data Analyst"}, head, base); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nil run: ожидали ErrNotFound, получили %v", err)
	}
}

// TestBuildRunCompareResponseDirection фиксирует направление сравнения:
// путь-исследование — head (added), against — base (removed).
func TestBuildRunCompareResponseDirection(t *testing.T) {
	base := []domain.Node{compareTestNode("node-base", "Есть только в базе", "src-1", "q")}
	head := []domain.Node{compareTestNode("node-head", "Есть только в голове", "src-1", "q")}
	result, err := buildRunCompareResponse(
		&domain.Run{ID: compareTestRunID, Profession: "Data Analyst"},
		&domain.Run{ID: compareTestAgainstID, Profession: "Data Analyst"},
		head, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Added) != 1 || result.Added[0].ID != "node-head" {
		t.Fatalf("added должен содержать узлы head: %+v", result.Added)
	}
	if len(result.Removed) != 1 || result.Removed[0].ID != "node-base" {
		t.Fatalf("removed должен содержать узлы base: %+v", result.Removed)
	}
}

// --- HTTP-слой ---

func TestCompareRunsHandlerAuth(t *testing.T) {
	stub := &runCompareStub{result: runCompareResponse{RunID: compareTestRunID, AgainstRunID: compareTestAgainstID, Profession: "Data Analyst"}}
	handler := runCompareTestHandler(NewServer(stub))
	tests := []struct {
		role string
		want int
	}{
		{"admin", http.StatusOK},
		{"teacher", http.StatusOK},
		{"rop", http.StatusOK},
		{"guest", http.StatusOK},
		{"student", http.StatusOK},
		{"", http.StatusUnauthorized},
	}
	for _, test := range tests {
		t.Run("role="+test.role, func(t *testing.T) {
			rec := do(t, handler, http.MethodGet, runCompareTestPath(compareTestRunID, compareTestAgainstID), test.role, "")
			if rec.Code != test.want {
				t.Fatalf("роль %q: ожидали %d, получили %d (%s)", test.role, test.want, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestCompareRunsHandlerAgainstValidation(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"missing", "/api/v1/runs/" + compareTestRunID + "/compare"},
		{"empty", "/api/v1/runs/" + compareTestRunID + "/compare?against="},
		{"blank", "/api/v1/runs/" + compareTestRunID + "/compare?against=%20%20"},
		{"invalid uuid", "/api/v1/runs/" + compareTestRunID + "/compare?against=not-a-uuid"},
		{"same as id", "/api/v1/runs/" + compareTestRunID + "/compare?against=" + compareTestRunID},
		{"same as id upper case", "/api/v1/runs/" + compareTestRunID + "/compare?against=" + strings.ToUpper(compareTestRunID)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &runCompareStub{}
			handler := runCompareTestHandler(NewServer(stub))
			rec := do(t, handler, http.MethodGet, test.path, "teacher", "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("ожидали 400, получили %d (%s)", rec.Code, rec.Body.String())
			}
			if stub.calls != 0 {
				t.Fatalf("хранилище не должно вызываться при невалидном запросе (calls=%d)", stub.calls)
			}
		})
	}
}

func TestCompareRunsHandlerInvalidPathID(t *testing.T) {
	stub := &runCompareStub{}
	handler := runCompareTestHandler(NewServer(stub))
	rec := do(t, handler, http.MethodGet, runCompareTestPath("not-a-uuid", compareTestAgainstID), "teacher", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("невалидный id в пути: ожидали 404, получили %d (%s)", rec.Code, rec.Body.String())
	}
	if stub.calls != 0 {
		t.Fatalf("хранилище не должно вызываться (calls=%d)", stub.calls)
	}
}

func TestCompareRunsHandlerStoreErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
		code string
	}{
		{"not found", ErrNotFound, http.StatusNotFound, "NOT_FOUND"},
		{"profession mismatch", ErrRunProfessionMismatch, http.StatusConflict, "RUN_PROFESSION_MISMATCH"},
		{"tree missing", ErrRunTreeMissing, http.StatusConflict, "RUN_TREE_MISSING"},
		{"service unavailable", ErrServiceUnavailable, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE"},
		{"internal", errors.New("boom"), http.StatusInternalServerError, "INTERNAL"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &runCompareStub{err: test.err}
			handler := runCompareTestHandler(NewServer(stub))
			rec := do(t, handler, http.MethodGet, runCompareTestPath(compareTestRunID, compareTestAgainstID), "teacher", "")
			if rec.Code != test.want {
				t.Fatalf("ожидали %d, получили %d (%s)", test.want, rec.Code, rec.Body.String())
			}
			var body errorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != test.code {
				t.Fatalf("ожидали код %q, получили %+v (%v)", test.code, body, err)
			}
		})
	}
}

func TestCompareRunsHandlerNotImplemented(t *testing.T) {
	// In-memory Store не умеет сравнивать исследования; параметры валидны, но
	// хранилище не реализует runComparer.
	handler := runCompareTestHandler(NewServer(newMemStore()))
	rec := do(t, handler, http.MethodGet, runCompareTestPath(compareTestRunID, compareTestAgainstID), "teacher", "")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("ожидали 501, получили %d (%s)", rec.Code, rec.Body.String())
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != "NOT_IMPLEMENTED" {
		t.Fatalf("ожидали NOT_IMPLEMENTED, получили %+v (%v)", body, err)
	}
}

func TestCompareRunsHandlerSuccess(t *testing.T) {
	parent := "node-parent"
	added := domain.Node{ID: "node-added", Type: domain.NodeFunction, Title: "Новая функция", ParentID: &parent,
		Evidence: []domain.Evidence{{ID: "ev-added", NodeID: "node-added", SourceID: "src-1", RefID: "msg-3", Quote: "новая цитата", Status: domain.EvidencePending}}}
	removed := domain.Node{ID: "node-removed", Type: domain.NodeFunction, Title: "Удалённая функция", ParentID: &parent,
		Evidence: []domain.Evidence{{ID: "ev-removed", NodeID: "node-removed", SourceID: "src-1", RefID: "msg-4", Quote: "удалённая цитата", Status: domain.EvidencePending}}}
	beforeNode := domain.Node{ID: "node-1", Type: domain.NodeRole, Title: "Роль",
		Evidence: []domain.Evidence{{ID: "ev-1", NodeID: "node-1", SourceID: "src-1", RefID: "msg-1", Quote: "цитата", Status: domain.EvidencePending}}}
	afterNode := domain.Node{ID: "node-1", Type: domain.NodeRole, Title: "Роль (уточнено)",
		Evidence: []domain.Evidence{{ID: "ev-1", NodeID: "node-1", SourceID: "src-1", RefID: "msg-1", Quote: "цитата", Status: domain.EvidenceApproved}}}
	stub := &runCompareStub{result: runCompareResponse{
		RunID:          compareTestRunID,
		AgainstRunID:   compareTestAgainstID,
		Profession:     "Data Analyst",
		SameProfession: true,
		Match:          runCompareMatch{ByID: 1, ByTitle: 2, Unmatched: 2},
		Added:          []domain.Node{added},
		Removed:        []domain.Node{removed},
		Changed:        []snapshotNodeChange{{ID: "node-1", Before: beforeNode, After: afterNode, Fields: []string{"title", "evidence"}}},
		Summary:        snapshotDiffSummary{Added: 1, Removed: 1, Changed: 1},
	}}
	handler := runCompareTestHandler(NewServer(stub))
	rec := do(t, handler, http.MethodGet, runCompareTestPath(compareTestRunID, compareTestAgainstID), "teacher", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", rec.Code, rec.Body.String())
	}

	// Контракт DTO: ровно документированный набор полей (story без story_lines).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("ответ не разобрался: %v", err)
	}
	for _, key := range []string{"run_id", "against_run_id", "profession", "same_profession", "match", "added", "removed", "changed", "summary", "story"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("в ответе нет поля %q: %s", key, rec.Body.String())
		}
	}
	if len(raw) != 10 {
		t.Fatalf("ответ содержит лишние поля: %s", rec.Body.String())
	}

	var got runCompareResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("ответ не разобрался: %v", err)
	}
	if got.RunID != compareTestRunID || got.AgainstRunID != compareTestAgainstID || got.Profession != "Data Analyst" {
		t.Fatalf("идентификаторы/профессия: %+v", got)
	}
	if !got.SameProfession || got.Match != (runCompareMatch{ByID: 1, ByTitle: 2, Unmatched: 2}) {
		t.Fatalf("same_profession/match: %+v", got)
	}
	if got.Summary != (snapshotDiffSummary{Added: 1, Removed: 1, Changed: 1}) {
		t.Fatalf("summary: %+v", got.Summary)
	}
	if len(got.Added) != 1 || got.Added[0].Evidence[0].SourceID != "src-1" || got.Added[0].Evidence[0].RefID != "msg-3" {
		t.Fatalf("added без source/ref: %+v", got.Added)
	}
	if len(got.Changed) != 1 || !reflect.DeepEqual(got.Changed[0].Fields, []string{"title", "evidence"}) {
		t.Fatalf("changed: %+v", got.Changed)
	}

	// Story собирает хендлер из тех же added/removed/changed.
	wantStory, _ := buildComparisonStory(got.Added, got.Removed, got.Changed)
	if got.Story != wantStory {
		t.Fatalf("story = %q, ожидали %q", got.Story, wantStory)
	}
	wantHeader := "Узлов добавлено: 1, удалено: 1, изменено: 1."
	if !strings.HasPrefix(got.Story, wantHeader) {
		t.Fatalf("story должна начинаться с заголовка %q: %q", wantHeader, got.Story)
	}
	if !strings.Contains(got.Story, "Изменён узел «Роль (уточнено)»: название, подтверждения.") {
		t.Fatalf("story не содержит подробностей: %q", got.Story)
	}
	if stub.calls != 1 || stub.runID != compareTestRunID || stub.againstID != compareTestAgainstID {
		t.Fatalf("хендлер передал неверные аргументы: calls=%d run=%q against=%q",
			stub.calls, stub.runID, stub.againstID)
	}
}

// TestCompareRunsHandlerEmptyDiffStory — идентичные деревья (пустой diff) дают
// человекочитаемое «изменений нет», а не пустую строку.
func TestCompareRunsHandlerEmptyDiffStory(t *testing.T) {
	stub := &runCompareStub{result: runCompareResponse{
		RunID: compareTestRunID, AgainstRunID: compareTestAgainstID, Profession: "Data Analyst",
		Added: []domain.Node{}, Removed: []domain.Node{}, Changed: []snapshotNodeChange{},
	}}
	handler := runCompareTestHandler(NewServer(stub))
	rec := do(t, handler, http.MethodGet, runCompareTestPath(compareTestRunID, compareTestAgainstID), "teacher", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", rec.Code, rec.Body.String())
	}
	var got runCompareResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Story != comparisonStoryEmptyText {
		t.Fatalf("story = %q, ожидали %q", got.Story, comparisonStoryEmptyText)
	}
}

// --- PostgreSQL integration ---

func compareTestPool(t *testing.T) *pgxpool.Pool {
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

func compareTestUUID(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// compareTestTreeSpec — содержимое дерева независимого прогона. Одинаковые
// spec, применённые к разным прогонам, дают одинаковое содержимое с разными
// UUID узлов/подтверждений — именно этот сценарий проверяет semantic matching.
type compareTestTreeSpec struct {
	RoleTitle           string
	FunctionTitle       string
	FunctionDescription string
	TaskTitle           string
	TaskDescription     string
}

// compareTestInsertTree создаёт дерево role → function → task с НОВЫМИ UUID и
// сохраняет его прогону. Цитаты/якоря/статусы подтверждений зависят только от
// spec, поэтому одинаковые spec дают семантически одинаковое содержимое.
func compareTestInsertTree(t *testing.T, ctx context.Context, p *PGStore, pool *pgxpool.Pool, runID, sourceID string, spec compareTestTreeSpec) []domain.Node {
	t.Helper()
	rootID := compareTestUUID(t, pool)
	nodes := []domain.Node{{
		ID: rootID, Type: domain.NodeRole, Title: spec.RoleTitle,
		Evidence: []domain.Evidence{{ID: compareTestUUID(t, pool), SourceID: sourceID, RefID: "msg-role", Quote: "цитата роли", Status: domain.EvidencePending}},
	}}
	if spec.FunctionTitle != "" {
		nodes = append(nodes, domain.Node{
			ID: compareTestUUID(t, pool), Type: domain.NodeFunction, Title: spec.FunctionTitle,
			Description: spec.FunctionDescription, ParentID: &rootID,
			Evidence: []domain.Evidence{{ID: compareTestUUID(t, pool), SourceID: sourceID, RefID: "msg-function", Quote: "цитата функции", Status: domain.EvidencePending}},
		})
	}
	if spec.TaskTitle != "" {
		parentID := nodes[1].ID
		nodes = append(nodes, domain.Node{
			ID: compareTestUUID(t, pool), Type: domain.NodeTask, Title: spec.TaskTitle,
			Description: spec.TaskDescription, ParentID: &parentID,
			Evidence: []domain.Evidence{{ID: compareTestUUID(t, pool), SourceID: sourceID, RefID: "msg-task", Quote: "цитата задачи", Status: domain.EvidencePending}},
		})
	}
	if err := p.SaveTree(ctx, runID, nodes); err != nil {
		t.Fatalf("SaveTree(%s): %v", runID, err)
	}
	return nodes
}

func TestCompareRunsЧерезAPI(t *testing.T) {
	pool := compareTestPool(t)
	ctx := context.Background()
	p := NewPGStore(pool)
	handler := runCompareTestHandler(NewServer(p))

	source, err := p.CreateSource(domain.Source{Type: domain.SourceDocument, Title: "Источник сравнения", Content: "Текст источника", SourceURL: "https://example.org/compare"})
	if err != nil {
		t.Fatal(err)
	}
	createRun := func(profession string) *domain.Run {
		t.Helper()
		run, createErr := p.CreateRun(profession)
		if createErr != nil {
			t.Fatal(createErr)
		}
		return run
	}
	runBase := createRun("Data Analyst")
	runIdentical := createRun("Data Analyst")
	runChanged := createRun("Data Analyst")
	runExtended := createRun("Data Analyst")
	runNoTree := createRun("Data Analyst")
	runOtherProfession := createRun("DevOps Engineer")
	runHeroRu := createRun("Аналитик данных")
	runHeroEn := createRun("Data Analyst")
	allRuns := []string{runBase.ID, runIdentical.ID, runChanged.ID, runExtended.ID,
		runNoTree.ID, runOtherProfession.ID, runHeroRu.ID, runHeroEn.ID}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM runs WHERE id = ANY($1::uuid[])`, allRuns)
		pool.Exec(context.Background(), `DELETE FROM sources WHERE id=$1`, source.ID)
	})

	// Содержимое прогонов: почти одинаковое (как два независимых анализа одной
	// профессии), но с разными UUID узлов.
	baseSpec := compareTestTreeSpec{RoleTitle: "Аналитик данных", FunctionTitle: "Отчётность", FunctionDescription: "Готовить отчёты"}
	baseTree := compareTestInsertTree(t, ctx, p, pool, runBase.ID, source.ID, baseSpec)
	identicalTree := compareTestInsertTree(t, ctx, p, pool, runIdentical.ID, source.ID, baseSpec)
	changedSpec := baseSpec
	changedSpec.FunctionDescription = "Готовить и проверять отчёты"
	_ = compareTestInsertTree(t, ctx, p, pool, runChanged.ID, source.ID, changedSpec)
	extendedSpec := baseSpec
	extendedSpec.TaskTitle = "Писать SQL-запросы"
	extendedTree := compareTestInsertTree(t, ctx, p, pool, runExtended.ID, source.ID, extendedSpec)
	_ = compareTestInsertTree(t, ctx, p, pool, runOtherProfession.ID, source.ID, compareTestTreeSpec{RoleTitle: "DevOps"})
	_ = compareTestInsertTree(t, ctx, p, pool, runHeroRu.ID, source.ID, compareTestTreeSpec{RoleTitle: "Аналитик данных"})
	_ = compareTestInsertTree(t, ctx, p, pool, runHeroEn.ID, source.ID, compareTestTreeSpec{RoleTitle: "Аналитик данных"})
	// Прогоны завершены — типичный сценарий фичи #11.
	if _, err = pool.Exec(ctx, `UPDATE runs SET status='done', stage='done' WHERE id = ANY($1::uuid[])`, allRuns); err != nil {
		t.Fatal(err)
	}

	compare := func(headID, againstID, role string) (*httptest.ResponseRecorder, runCompareResponse) {
		t.Helper()
		rec := do(t, handler, http.MethodGet, runCompareTestPath(headID, againstID), role, "")
		var result runCompareResponse
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatalf("ответ не разобрался: %v (%s)", err, rec.Body.String())
			}
		}
		return rec, result
	}

	// 1) Два независимых прогона с одинаковым содержимым и разными UUID:
	// semantic matching обязан дать пустой diff, а не «всё added/removed».
	rec, identical := compare(runIdentical.ID, runBase.ID, "teacher")
	if rec.Code != http.StatusOK {
		t.Fatalf("identical: %d %s", rec.Code, rec.Body.String())
	}
	if identical.RunID != runIdentical.ID || identical.AgainstRunID != runBase.ID || identical.Profession != "Data Analyst" {
		t.Fatalf("run/against/profession: %+v", identical)
	}
	if !identical.SameProfession {
		t.Fatalf("same_profession должен быть true: %+v", identical)
	}
	if identical.Summary != (snapshotDiffSummary{}) {
		t.Fatalf("одинаковое содержимое не должно давать diff: %+v", identical.Summary)
	}
	if identical.Match != (runCompareMatch{ByID: 0, ByTitle: len(baseTree), Unmatched: 0}) {
		t.Fatalf("match: %+v (base nodes=%d)", identical.Match, len(baseTree))
	}
	if identical.Story != comparisonStoryEmptyText {
		t.Fatalf("story одинаковых прогонов: %q", identical.Story)
	}
	for _, node := range identical.Added {
		t.Fatalf("added должен быть пуст, получили %+v", node)
	}

	// 2) Одинаковые type/title/путь, разные description → ровно один changed.
	rec, changed := compare(runChanged.ID, runIdentical.ID, "teacher")
	if rec.Code != http.StatusOK {
		t.Fatalf("changed: %d %s", rec.Code, rec.Body.String())
	}
	if changed.Summary != (snapshotDiffSummary{Changed: 1}) || len(changed.Changed) != 1 {
		t.Fatalf("changed summary: %+v, changed=%+v", changed.Summary, changed.Changed)
	}
	change := changed.Changed[0]
	if !reflect.DeepEqual(change.Fields, []string{"description"}) {
		t.Fatalf("поля изменения: %v", change.Fields)
	}
	if change.Before.Description != "Готовить отчёты" || change.After.Description != "Готовить и проверять отчёты" {
		t.Fatalf("before/after description: %q -> %q", change.Before.Description, change.After.Description)
	}
	if change.Before.ID == change.After.ID {
		t.Fatalf("у semantic-пары разные UUID узлов в прогонах: %q", change.Before.ID)
	}
	if change.After.Evidence[0].SourceID != source.ID || change.After.Evidence[0].RefID != "msg-function" {
		t.Fatalf("evidence сохранён: %+v", change.After.Evidence)
	}
	if changed.Match.ByID != 0 || changed.Match.ByTitle != len(baseTree) || changed.Match.Unmatched != 0 {
		t.Fatalf("match: %+v", changed.Match)
	}
	wantChangedHeader := "Узлов добавлено: 0, удалено: 0, изменено: 1."
	if !strings.HasPrefix(changed.Story, wantChangedHeader) ||
		!strings.Contains(changed.Story, "Изменён узел «Отчётность»: описание.") {
		t.Fatalf("story changed: %q", changed.Story)
	}

	// 3) added/removed на независимых прогонах: у extended появился task.
	rec, extended := compare(runExtended.ID, runBase.ID, "teacher")
	if rec.Code != http.StatusOK {
		t.Fatalf("extended: %d %s", rec.Code, rec.Body.String())
	}
	if extended.Summary != (snapshotDiffSummary{Added: 1}) || len(extended.Added) != 1 {
		t.Fatalf("added summary: %+v (%+v)", extended.Summary, extended.Added)
	}
	if extended.Added[0].ID != extendedTree[2].ID || extended.Added[0].Title != "Писать SQL-запросы" {
		t.Fatalf("added node: %+v", extended.Added[0])
	}
	if extended.Added[0].Evidence[0].SourceID != source.ID || extended.Added[0].Evidence[0].RefID != "msg-task" {
		t.Fatalf("added без ссылки на источник: %+v", extended.Added[0].Evidence)
	}
	if extended.Match != (runCompareMatch{ByID: 0, ByTitle: len(baseTree), Unmatched: 1}) {
		t.Fatalf("match extended: %+v", extended.Match)
	}
	rec, reversed := compare(runBase.ID, runExtended.ID, "teacher")
	if rec.Code != http.StatusOK || reversed.Summary != (snapshotDiffSummary{Removed: 1}) {
		t.Fatalf("обратное сравнение: %d %+v", rec.Code, reversed.Summary)
	}
	if len(reversed.Removed) != 1 || reversed.Removed[0].Title != "Писать SQL-запросы" {
		t.Fatalf("removed: %+v", reversed.Removed)
	}

	// 4) Hero-пара: profession-строки разные, но корневой role-узел совпадает —
	// сравнение разрешено (409 не должно быть).
	rec, hero := compare(runHeroEn.ID, runHeroRu.ID, "teacher")
	if rec.Code != http.StatusOK {
		t.Fatalf("hero pair: ожидали 200, получили %d %s", rec.Code, rec.Body.String())
	}
	if !hero.SameProfession {
		t.Fatalf("hero pair same_profession: %+v", hero)
	}
	if hero.Match != (runCompareMatch{ByTitle: 1}) {
		t.Fatalf("hero pair match: %+v", hero.Match)
	}

	// 5) Разные профессии И разные корни — 409.
	rec, _ = compare(runHeroEn.ID, runOtherProfession.ID, "teacher")
	if rec.Code != http.StatusConflict {
		t.Fatalf("разные профессии: ожидали 409, получили %d %s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "RUN_PROFESSION_MISMATCH")

	// 6) Нет дерева — не выдумываем данные, 409 RUN_TREE_MISSING.
	rec, _ = compare(runBase.ID, runNoTree.ID, "teacher")
	if rec.Code != http.StatusConflict {
		t.Fatalf("нет дерева: ожидали 409, получили %d %s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "RUN_TREE_MISSING")

	// 7) Неизвестные исследования — 404; невалидный against — 400; id=against — 400.
	randomID := compareTestUUID(t, pool)
	for _, test := range []struct {
		name string
		path string
		want int
	}{
		{"unknown against", runCompareTestPath(runBase.ID, randomID), http.StatusNotFound},
		{"unknown run", runCompareTestPath(randomID, runBase.ID), http.StatusNotFound},
		{"invalid against", "/api/v1/runs/" + runBase.ID + "/compare?against=oops", http.StatusBadRequest},
		{"against == id", runCompareTestPath(runBase.ID, runBase.ID), http.StatusBadRequest},
	} {
		rec := do(t, handler, http.MethodGet, test.path, "teacher", "")
		if rec.Code != test.want {
			t.Fatalf("%s: ожидали %d, получили %d (%s)", test.name, test.want, rec.Code, rec.Body.String())
		}
	}

	// 8) Права чтения деревьев: guest читает, student нет, без токена — 401.
	path := runCompareTestPath(runIdentical.ID, runBase.ID)
	if rec = do(t, handler, http.MethodGet, path, "guest", ""); rec.Code != http.StatusOK {
		t.Fatalf("guest: %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(t, handler, http.MethodGet, path, "student", ""); rec.Code != http.StatusOK {
		t.Fatalf("student: ожидали 200, получили %d", rec.Code)
	}
	if rec = do(t, handler, http.MethodGet, path, "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("без токена: ожидали 401, получили %d", rec.Code)
	}

	// 9) Сравнение только читает: деревья после всех запросов не изменились
	// (GetTree возвращает узлы в порядке level,id, поэтому сверяем состав).
	afterBase, err := p.GetTree(runBase.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !sameNodeIDSet(afterBase, baseTree) {
		t.Fatalf("дерево base изменилось после сравнения: %+v", afterBase)
	}
	afterIdentical, err := p.GetTree(runIdentical.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !sameNodeIDSet(afterIdentical, identicalTree) {
		t.Fatalf("дерево identical изменилось после сравнения: %+v", afterIdentical)
	}
}

// --- helpers ---

func sameNodeIDSet(a, b []domain.Node) bool {
	if len(a) != len(b) {
		return false
	}
	ids := make(map[string]bool, len(a))
	for _, node := range a {
		ids[node.ID] = true
	}
	for _, node := range b {
		if !ids[node.ID] {
			return false
		}
	}
	return true
}
