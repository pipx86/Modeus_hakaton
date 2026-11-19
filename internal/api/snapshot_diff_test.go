package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	snapshotDiffTestRunID   = "11111111-1111-4111-8111-111111111111"
	snapshotDiffTestHeadID  = "22222222-2222-4222-8222-222222222222"
	snapshotDiffTestBaseID  = "33333333-3333-4333-8333-333333333333"
	snapshotDiffTestOtherID = "44444444-4444-4444-8444-444444444444"
)

// snapshotDiffStub — заглушка snapshotDiffer для проверки HTTP-слоя без БД.
type snapshotDiffStub struct {
	Store
	calls  int
	runID  string
	headID string
	baseID string
	result snapshotDiffResponse
	err    error
}

func (s *snapshotDiffStub) snapshotDiff(_ context.Context, runID, headID, baseID string) (snapshotDiffResponse, error) {
	s.calls++
	s.runID, s.headID, s.baseID = runID, headID, baseID
	return s.result, s.err
}

func snapshotDiffTestPath(runID, headID string) string {
	return "/api/v1/runs/" + runID + "/snapshots/" + headID + "/diff?against=" + snapshotDiffTestBaseID
}

func TestParseSnapshotTree(t *testing.T) {
	raw := snapshotDiffTreeJSON(t, map[string]any{
		"schema_version": 1,
		"root_node_id":   "node-role",
		"nodes": []any{
			map[string]any{
				"id":          "node-role",
				"parent_id":   nil,
				"level":       0,
				"type":        "role",
				"title":       "Роль",
				"description": "Исходное описание",
				"evidence": []any{
					map[string]any{
						"id": "ev-2", "ref_id": "msg-2", "quote": "вторая цитата", "approved": true,
						"source": map[string]any{"id": "src-1", "type": "document", "title": "Источник", "source_url": "https://example.org/doc"},
					},
					map[string]any{
						"id": "ev-1", "ref_id": "msg-1", "quote": "первая цитата", "approved": false,
						"source": map[string]any{"id": "src-1", "type": "document", "title": "Источник", "source_url": nil},
					},
				},
			},
			map[string]any{
				"id": "node-function", "parent_id": "node-role", "level": 1, "type": "function",
				"title": "Функция", "description": nil,
				"evidence": []any{
					map[string]any{"id": "ev-3", "source_id": "src-2", "status": "rejected", "ref_id": nil, "quote": "третья цитата"},
				},
			},
		},
	})
	nodes, err := parseSnapshotTree(raw)
	if err != nil {
		t.Fatalf("parseSnapshotTree: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("ожидали 2 узла, получили %d: %+v", len(nodes), nodes)
	}
	root := nodes[0]
	if root.ID != "node-role" || root.Type != domain.NodeRole || root.Title != "Роль" ||
		root.Description != "Исходное описание" || root.ParentID != nil {
		t.Fatalf("корень разобран неверно: %+v", root)
	}
	if len(root.Evidence) != 2 || root.Evidence[0].ID != "ev-1" || root.Evidence[1].ID != "ev-2" {
		t.Fatalf("подтверждения должны быть отсортированы по id: %+v", root.Evidence)
	}
	first, second := root.Evidence[0], root.Evidence[1]
	if first.NodeID != "node-role" || first.SourceID != "src-1" || first.RefID != "msg-1" ||
		first.Quote != "первая цитата" || first.Status != domain.EvidencePending {
		t.Fatalf("pending-подтверждение разобрано неверно: %+v", first)
	}
	if second.Status != domain.EvidenceApproved || second.RefID != "msg-2" {
		t.Fatalf("approved-подтверждение разобрано неверно: %+v", second)
	}
	function := nodes[1]
	if function.ID != "node-function" || function.Type != domain.NodeFunction || function.ParentID == nil || *function.ParentID != "node-role" {
		t.Fatalf("дочерний узел разобран неверно: %+v", function)
	}
	if function.Description != "" {
		t.Fatalf("null description должен давать пустую строку, получили %q", function.Description)
	}
	if len(function.Evidence) != 1 || function.Evidence[0].SourceID != "src-2" ||
		function.Evidence[0].Status != domain.EvidenceRejected || function.Evidence[0].RefID != "" {
		t.Fatalf("плоский формат evidence разобран неверно: %+v", function.Evidence)
	}
}

func TestParseSnapshotTreeEmpty(t *testing.T) {
	raw := snapshotDiffTreeJSON(t, map[string]any{"schema_version": 1, "nodes": []any{}})
	nodes, err := parseSnapshotTree(raw)
	if err != nil {
		t.Fatalf("parseSnapshotTree: %v", err)
	}
	if len(nodes) != 0 || nodes == nil {
		t.Fatalf("ожидали пустой не-nil список, получили %#v", nodes)
	}
}

func TestParseSnapshotTreeInvalid(t *testing.T) {
	validNode := func() map[string]any {
		return map[string]any{
			"id": "node-1", "type": "role", "title": "Роль",
			"evidence": []any{map[string]any{"id": "ev-1", "source_id": "src-1", "quote": "цитата"}},
		}
	}
	tests := []struct {
		name    string
		raw     json.RawMessage
		wantSub string
	}{
		{"empty", json.RawMessage("  "), "пустое дерево"},
		{"not json", json.RawMessage("{"), "SNAPSHOT_TREE_INVALID"},
		{"node without id", snapshotDiffTreeJSON(t, map[string]any{"nodes": []any{map[string]any{"type": "role", "title": "Роль", "evidence": []any{}}}}), "без идентификатора"},
		{"duplicate node", snapshotDiffTreeJSON(t, map[string]any{"nodes": []any{validNode(), validNode()}}), "повторяется узел"},
		{"evidence without id", snapshotDiffTreeJSON(t, map[string]any{"nodes": []any{map[string]any{"id": "node-1", "type": "role", "title": "Роль", "evidence": []any{map[string]any{"quote": "цитата"}}}}}), "подтверждение без идентификатора"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseSnapshotTree(test.raw)
			if err == nil {
				t.Fatal("ожидали ошибку разбора")
			}
			if !errors.Is(err, errSnapshotTreeInvalid) || !strings.Contains(err.Error(), test.wantSub) {
				t.Fatalf("неожиданная ошибка: %v (ожидали %q)", err, test.wantSub)
			}
		})
	}
}

func TestDiffSnapshotTrees(t *testing.T) {
	base := []domain.Node{
		diffTestNode("node-b", "Узел Б", "q-b", domain.EvidencePending),
		diffTestNode("node-a", "Узел А", "q-a", domain.EvidencePending),
		diffTestNode("node-removed", "Удалённый", "q-r", domain.EvidencePending),
	}
	head := []domain.Node{
		diffTestNode("node-added", "Добавленный", "q-n", domain.EvidencePending),
		diffTestNode("node-b", "Узел Б", "q-b", domain.EvidencePending),
		diffTestNode("node-a", "Узел А (новое название)", "q-a", domain.EvidencePending),
	}
	result := diffSnapshotTrees(base, head)
	if len(result.Added) != 1 || result.Added[0].ID != "node-added" {
		t.Fatalf("added: %+v", result.Added)
	}
	if len(result.Removed) != 1 || result.Removed[0].ID != "node-removed" {
		t.Fatalf("removed: %+v", result.Removed)
	}
	if len(result.Changed) != 1 || result.Changed[0].ID != "node-a" {
		t.Fatalf("changed: %+v", result.Changed)
	}
	if !reflect.DeepEqual(result.Changed[0].Fields, []string{"title"}) {
		t.Fatalf("поля изменения: %v", result.Changed[0].Fields)
	}
	if result.Changed[0].Before.Title != "Узел А" || result.Changed[0].After.Title != "Узел А (новое название)" {
		t.Fatalf("before/after: %+v", result.Changed[0])
	}
}

func TestDiffSnapshotTreesFieldOrderAndParent(t *testing.T) {
	parentA, parentB := "node-parent-a", "node-parent-b"
	before := domain.Node{ID: "node-1", Type: domain.NodeRole, Title: "T1", Description: "D1", ParentID: &parentA,
		Evidence: []domain.Evidence{{ID: "ev-1", NodeID: "node-1", SourceID: "src-1", RefID: "r1", Quote: "q1", Status: domain.EvidencePending}}}
	after := domain.Node{ID: "node-1", Type: domain.NodeTask, Title: "T2", Description: "D2", ParentID: &parentB,
		Evidence: []domain.Evidence{{ID: "ev-1", NodeID: "node-1", SourceID: "src-2", RefID: "r2", Quote: "q2", Status: domain.EvidenceApproved}}}
	result := diffSnapshotTrees([]domain.Node{before}, []domain.Node{after})
	want := []string{"title", "description", "type", "parent_id", "evidence"}
	if len(result.Changed) != 1 || !reflect.DeepEqual(result.Changed[0].Fields, want) {
		t.Fatalf("ожидали поля %v, получили %+v", want, result.Changed)
	}
}

func TestDiffSnapshotTreesEvidenceChanges(t *testing.T) {
	baseEvidence := domain.Evidence{ID: "ev-1", NodeID: "node-1", SourceID: "src-1", RefID: "msg-1", Quote: "цитата", Status: domain.EvidencePending}
	base := domain.Node{ID: "node-1", Type: domain.NodeRole, Title: "Роль", Evidence: []domain.Evidence{baseEvidence}}
	tests := []struct {
		name   string
		change func(domain.Evidence) domain.Evidence
		want   bool
	}{
		{"quote", func(e domain.Evidence) domain.Evidence { e.Quote = "другая"; return e }, true},
		{"source_id", func(e domain.Evidence) domain.Evidence { e.SourceID = "src-2"; return e }, true},
		{"ref_id", func(e domain.Evidence) domain.Evidence { e.RefID = "msg-2"; return e }, true},
		{"status", func(e domain.Evidence) domain.Evidence { e.Status = domain.EvidenceRejected; return e }, true},
		{"empty status is pending", func(e domain.Evidence) domain.Evidence { e.Status = ""; return e }, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			headEvidence := test.change(baseEvidence)
			head := domain.Node{ID: "node-1", Type: domain.NodeRole, Title: "Роль", Evidence: []domain.Evidence{headEvidence}}
			changed := len(diffSnapshotTrees([]domain.Node{base}, []domain.Node{head}).Changed) == 1
			if changed != test.want {
				t.Fatalf("changed = %v, ожидали %v", changed, test.want)
			}
		})
	}
	// Порядок подтверждений не считается изменением состава/полей.
	second := domain.Evidence{ID: "ev-0", NodeID: "node-1", SourceID: "src-1", RefID: "msg-0", Quote: "нулевая", Status: domain.EvidenceApproved}
	ordered := domain.Node{ID: "node-1", Type: domain.NodeRole, Title: "Роль", Evidence: []domain.Evidence{baseEvidence, second}}
	reordered := domain.Node{ID: "node-1", Type: domain.NodeRole, Title: "Роль", Evidence: []domain.Evidence{second, baseEvidence}}
	if changed := diffSnapshotTrees([]domain.Node{ordered}, []domain.Node{reordered}); len(changed.Changed) != 0 {
		t.Fatalf("порядок evidence не должен считаться изменением: %+v", changed.Changed)
	}
}

func TestDiffSnapshotTreesEmpty(t *testing.T) {
	result := diffSnapshotTrees(nil, nil)
	if result.Added == nil || result.Removed == nil || result.Changed == nil {
		t.Fatalf("срезы должны быть не-nil: %+v", result)
	}
	if len(result.Added)+len(result.Removed)+len(result.Changed) != 0 {
		t.Fatalf("одинаковые снимки не должны давать diff: %+v", result)
	}
}

func TestSnapshotDiffHandlerAuth(t *testing.T) {
	stub := &snapshotDiffStub{result: snapshotDiffResponse{RunID: snapshotDiffTestRunID}}
	handler := NewServer(stub).Routes()
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
			rec := do(t, handler, http.MethodGet, snapshotDiffTestPath(snapshotDiffTestRunID, snapshotDiffTestHeadID), test.role, "")
			if rec.Code != test.want {
				t.Fatalf("роль %q: ожидали %d, получили %d (%s)", test.role, test.want, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestSnapshotDiffHandlerAgainstValidation(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"missing", "/api/v1/runs/" + snapshotDiffTestRunID + "/snapshots/" + snapshotDiffTestHeadID + "/diff"},
		{"empty", "/api/v1/runs/" + snapshotDiffTestRunID + "/snapshots/" + snapshotDiffTestHeadID + "/diff?against="},
		{"blank", "/api/v1/runs/" + snapshotDiffTestRunID + "/snapshots/" + snapshotDiffTestHeadID + "/diff?against=%20%20"},
		{"invalid uuid", "/api/v1/runs/" + snapshotDiffTestRunID + "/snapshots/" + snapshotDiffTestHeadID + "/diff?against=not-a-uuid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &snapshotDiffStub{}
			handler := NewServer(stub).Routes()
			rec := do(t, handler, http.MethodGet, test.path, "teacher", "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("ожидали 400, получили %d (%s)", rec.Code, rec.Body.String())
			}
			if stub.calls != 0 {
				t.Fatalf("хранилище не должно вызываться при невалидном against (calls=%d)", stub.calls)
			}
		})
	}
}

func TestSnapshotDiffHandlerStoreErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
		code string
	}{
		{"not found", ErrNotFound, http.StatusNotFound, "NOT_FOUND"},
		{"foreign run", ErrSnapshotForeignRun, http.StatusConflict, "SNAPSHOT_RUN_MISMATCH"},
		{"invalid tree", errSnapshotTreeInvalid, http.StatusInternalServerError, "SNAPSHOT_TREE_INVALID"},
		{"internal", errors.New("boom"), http.StatusInternalServerError, "INTERNAL"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &snapshotDiffStub{err: test.err}
			handler := NewServer(stub).Routes()
			rec := do(t, handler, http.MethodGet, snapshotDiffTestPath(snapshotDiffTestRunID, snapshotDiffTestHeadID), "teacher", "")
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

func TestSnapshotDiffHandlerValidationBeforeStorage(t *testing.T) {
	// In-memory store не умеет снимки, но невалидный запрос всё равно 400:
	// параметры проверяются до выбора бэкенда.
	handler := NewServer(newMemStore()).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+snapshotDiffTestRunID+"/snapshots/"+snapshotDiffTestHeadID+"/diff", "teacher", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ожидали 400 при отсутствии against на in-memory store, получили %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestSnapshotDiffHandlerNotImplemented(t *testing.T) {
	handler := NewServer(newMemStore()).Routes()
	rec := do(t, handler, http.MethodGet, snapshotDiffTestPath(snapshotDiffTestRunID, snapshotDiffTestHeadID), "teacher", "")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("in-memory store: ожидали 501, получили %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestSnapshotDiffHandlerSuccess(t *testing.T) {
	parent := "node-parent"
	added := domain.Node{ID: "node-added", Type: domain.NodeFunction, Title: "Новая функция", ParentID: &parent,
		Evidence: []domain.Evidence{{ID: "ev-added", NodeID: "node-added", SourceID: "src-1", RefID: "msg-3", Quote: "новая цитата", Status: domain.EvidencePending}}}
	removed := domain.Node{ID: "node-removed", Type: domain.NodeFunction, Title: "Удалённая функция", ParentID: &parent,
		Evidence: []domain.Evidence{{ID: "ev-removed", NodeID: "node-removed", SourceID: "src-1", RefID: "msg-4", Quote: "удалённая цитата", Status: domain.EvidencePending}}}
	beforeNode := domain.Node{ID: "node-1", Type: domain.NodeRole, Title: "Роль",
		Evidence: []domain.Evidence{{ID: "ev-1", NodeID: "node-1", SourceID: "src-1", RefID: "msg-1", Quote: "цитата", Status: domain.EvidencePending}}}
	afterNode := domain.Node{ID: "node-1", Type: domain.NodeRole, Title: "Роль (уточнено)",
		Evidence: []domain.Evidence{{ID: "ev-1", NodeID: "node-1", SourceID: "src-1", RefID: "msg-1", Quote: "цитата", Status: domain.EvidencePending}}}
	stub := &snapshotDiffStub{result: snapshotDiffResponse{
		RunID: snapshotDiffTestRunID,
		Base:  snapshotVersionRef{ID: snapshotDiffTestBaseID, Version: 1},
		Head:  snapshotVersionRef{ID: snapshotDiffTestHeadID, Version: 2},
		Added: []domain.Node{added}, Removed: []domain.Node{removed},
		Changed: []snapshotNodeChange{{ID: "node-1", Before: beforeNode, After: afterNode, Fields: []string{"title"}}},
		Summary: snapshotDiffSummary{Added: 1, Removed: 1, Changed: 1},
	}}
	handler := NewServer(stub).Routes()
	rec := do(t, handler, http.MethodGet, snapshotDiffTestPath(snapshotDiffTestRunID, snapshotDiffTestHeadID), "teacher", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", rec.Code, rec.Body.String())
	}
	var got snapshotDiffResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("ответ не разобрался: %v", err)
	}
	if got.RunID != snapshotDiffTestRunID || got.Base.ID != snapshotDiffTestBaseID || got.Head.Version != 2 {
		t.Fatalf("run/base/head: %+v", got)
	}
	if got.Summary != (snapshotDiffSummary{Added: 1, Removed: 1, Changed: 1}) {
		t.Fatalf("summary: %+v", got.Summary)
	}
	if len(got.Added) != 1 || got.Added[0].Evidence[0].RefID != "msg-3" || got.Added[0].Evidence[0].Status != domain.EvidencePending {
		t.Fatalf("added: %+v", got.Added)
	}
	if len(got.Changed) != 1 || !reflect.DeepEqual(got.Changed[0].Fields, []string{"title"}) ||
		got.Changed[0].After.Evidence[0].SourceID != "src-1" {
		t.Fatalf("changed: %+v", got.Changed)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"added":[`) || !strings.Contains(body, `"removed":[`) || !strings.Contains(body, `"changed":[`) {
		t.Fatalf("ожидали JSON-массивы в ответе: %s", body)
	}
	if stub.calls != 1 || stub.runID != snapshotDiffTestRunID || stub.headID != snapshotDiffTestHeadID || stub.baseID != snapshotDiffTestBaseID {
		t.Fatalf("хендлер передал неверные аргументы: calls=%d run=%q head=%q base=%q",
			stub.calls, stub.runID, stub.headID, stub.baseID)
	}
}

// --- helpers ---

func snapshotDiffTreeJSON(t *testing.T, document map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal snapshot tree: %v", err)
	}
	return raw
}

func diffTestNode(id, title, quote string, status domain.EvidenceStatus) domain.Node {
	return domain.Node{
		ID: id, Type: domain.NodeRole, Title: title,
		Evidence: []domain.Evidence{{ID: "ev-" + id, NodeID: id, SourceID: "src-1", RefID: "ref-" + id, Quote: quote, Status: status}},
	}
}

// --- PostgreSQL integration ---

func snapshotDiffTestPool(t *testing.T) *pgxpool.Pool {
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

func snapshotDiffTestUUID(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

type snapshotDiffCreated struct {
	ID      string `json:"id"`
	Version int32  `json:"version"`
}

func snapshotDiffCreate(t *testing.T, handler http.Handler, runID string) snapshotDiffCreated {
	t.Helper()
	rec := do(t, handler, http.MethodPost, "/api/v1/runs/"+runID+"/snapshots", "teacher", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("создание снимка: %d %s", rec.Code, rec.Body.String())
	}
	var created snapshotDiffCreated
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.ID == "" || created.Version == 0 {
		t.Fatalf("снимок не разобрался: %s (%v)", rec.Body.String(), err)
	}
	return created
}

// snapshotDiffInsertFunction создаёт дочерний узел типа function вместе с
// подтверждением одной транзакцией: отложенные constraint-триггеры дерева
// проверяют полноту на коммите.
func snapshotDiffInsertFunction(t *testing.T, pool *pgxpool.Pool, runID, parentID, nodeID, evidenceID, sourceID, title, quote, refID string) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO nodes(id,run_id,parent_id,level,type,title) VALUES($1,$2,$3,1,'function',$4)`,
		nodeID, runID, parentID, title); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO evidence(id,node_id,source_id,quote,ref_id) VALUES($1,$2,$3,$4,$5)`,
		evidenceID, nodeID, sourceID, quote, refID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotDiffЧерезAPI(t *testing.T) {
	pool := snapshotDiffTestPool(t)
	ctx := context.Background()
	p := NewPGStore(pool)
	handler := NewServer(p).Routes()

	source, err := p.CreateSource(domain.Source{Type: domain.SourceDocument, Title: "Источник снимков", Content: "Текст источника", SourceURL: "https://example.org/snapshot-diff"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.CreateRun("Сравнение снимков")
	if err != nil {
		t.Fatal(err)
	}
	otherRun, err := p.CreateRun("Другое исследование для снимков")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM runs WHERE id = ANY($1::uuid[])`, []string{run.ID, otherRun.ID})
		pool.Exec(context.Background(), `DELETE FROM sources WHERE id=$1`, source.ID)
	})

	rootID := snapshotDiffTestUUID(t, pool)
	removedID := snapshotDiffTestUUID(t, pool)
	addedID := snapshotDiffTestUUID(t, pool)
	rootEvidenceID := snapshotDiffTestUUID(t, pool)
	removedEvidenceID := snapshotDiffTestUUID(t, pool)
	addedEvidenceID := snapshotDiffTestUUID(t, pool)

	nodes := []domain.Node{
		{ID: rootID, Type: domain.NodeRole, Title: "Роль", Description: "Исходное описание",
			Evidence: []domain.Evidence{{ID: rootEvidenceID, SourceID: source.ID, RefID: "msg-1", Quote: "Исходная цитата"}}},
		{ID: removedID, Type: domain.NodeFunction, Title: "Функция", ParentID: &rootID,
			Evidence: []domain.Evidence{{ID: removedEvidenceID, SourceID: source.ID, RefID: "msg-2", Quote: "Цитата функции"}}},
	}
	if err = p.SaveTree(ctx, run.ID, nodes); err != nil {
		t.Fatal(err)
	}
	first := snapshotDiffCreate(t, handler, run.ID)

	// Меняем состояние: подтверждение подтверждено, заголовок уточнён, дочерний
	// узел удалён, вместо него добавлен новый.
	if err = p.ApproveEvidence(rootEvidenceID); err != nil {
		t.Fatal(err)
	}
	newTitle := "Роль (уточнено)"
	if _, err = p.UpdateNode(rootID, NodePatch{Title: &newTitle}); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM nodes WHERE id=$1`, removedID); err != nil {
		t.Fatal(err)
	}
	snapshotDiffInsertFunction(t, pool, run.ID, rootID, addedID, addedEvidenceID, source.ID, "Новая функция", "Цитата новой функции", "msg-3")
	second := snapshotDiffCreate(t, handler, run.ID)
	if first.Version != 1 || second.Version != 2 {
		t.Fatalf("версии снимков: %+v %+v", first, second)
	}

	path := "/api/v1/runs/" + run.ID + "/snapshots/" + second.ID + "/diff?against=" + first.ID
	rec := do(t, handler, http.MethodGet, path, "teacher", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("diff: %d %s", rec.Code, rec.Body.String())
	}
	var diff snapshotDiffResponse
	if err = json.Unmarshal(rec.Body.Bytes(), &diff); err != nil {
		t.Fatalf("diff не разобрался: %v (%s)", err, rec.Body.String())
	}
	if diff.RunID != run.ID || diff.Base.ID != first.ID || diff.Base.Version != 1 || diff.Head.ID != second.ID || diff.Head.Version != 2 {
		t.Fatalf("run/base/head: %+v", diff)
	}
	if diff.Summary != (snapshotDiffSummary{Added: 1, Removed: 1, Changed: 1}) {
		t.Fatalf("summary: %+v", diff.Summary)
	}
	if len(diff.Added) != 1 || diff.Added[0].ID != addedID || diff.Added[0].ParentID == nil || *diff.Added[0].ParentID != rootID {
		t.Fatalf("added: %+v", diff.Added)
	}
	if len(diff.Added[0].Evidence) != 1 || diff.Added[0].Evidence[0].SourceID != source.ID ||
		diff.Added[0].Evidence[0].RefID != "msg-3" || diff.Added[0].Evidence[0].Quote != "Цитата новой функции" {
		t.Fatalf("добавленный узел без ссылки на источник: %+v", diff.Added[0].Evidence)
	}
	if len(diff.Removed) != 1 || diff.Removed[0].ID != removedID {
		t.Fatalf("removed: %+v", diff.Removed)
	}
	if len(diff.Removed[0].Evidence) != 1 || diff.Removed[0].Evidence[0].SourceID != source.ID || diff.Removed[0].Evidence[0].RefID != "msg-2" {
		t.Fatalf("удалённый узел без ссылки на источник: %+v", diff.Removed[0].Evidence)
	}
	if len(diff.Changed) != 1 || diff.Changed[0].ID != rootID {
		t.Fatalf("changed: %+v", diff.Changed)
	}
	change := diff.Changed[0]
	if !reflect.DeepEqual(change.Fields, []string{"title", "evidence"}) {
		t.Fatalf("поля изменения: %v", change.Fields)
	}
	if change.Before.Title != "Роль" || change.After.Title != "Роль (уточнено)" {
		t.Fatalf("before/after title: %q -> %q", change.Before.Title, change.After.Title)
	}
	if len(change.Before.Evidence) != 1 || change.Before.Evidence[0].Status != domain.EvidencePending {
		t.Fatalf("статус до: %+v", change.Before.Evidence)
	}
	if len(change.After.Evidence) != 1 || change.After.Evidence[0].Status != domain.EvidenceApproved ||
		change.After.Evidence[0].RefID != "msg-1" || change.After.Evidence[0].Quote != "Исходная цитата" {
		t.Fatalf("статус/ссылка после: %+v", change.After.Evidence)
	}

	// Обратный diff: head/base меняются местами.
	reverse := do(t, handler, http.MethodGet,
		"/api/v1/runs/"+run.ID+"/snapshots/"+first.ID+"/diff?against="+second.ID, "teacher", "")
	if reverse.Code != http.StatusOK {
		t.Fatalf("обратный diff: %d %s", reverse.Code, reverse.Body.String())
	}
	var reverseDiff snapshotDiffResponse
	if err = json.Unmarshal(reverse.Body.Bytes(), &reverseDiff); err != nil {
		t.Fatal(err)
	}
	if reverseDiff.Summary != (snapshotDiffSummary{Added: 1, Removed: 1, Changed: 1}) ||
		len(reverseDiff.Added) != 1 || reverseDiff.Added[0].ID != removedID ||
		len(reverseDiff.Removed) != 1 || reverseDiff.Removed[0].ID != addedID {
		t.Fatalf("обратный diff: %+v", reverseDiff)
	}

	// Ошибки адресации и прав.
	if rec = do(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID+"/snapshots/"+second.ID+"/diff", "teacher", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("без against: %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID+"/snapshots/"+second.ID+"/diff?against=oops", "teacher", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("невалидный against: %d", rec.Code)
	}
	randomID := snapshotDiffTestUUID(t, pool)
	if rec = do(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID+"/snapshots/"+second.ID+"/diff?against="+randomID, "teacher", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("неизвестный снимок: %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(t, handler, http.MethodGet, "/api/v1/runs/"+randomID+"/snapshots/"+second.ID+"/diff?against="+first.ID, "teacher", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("неизвестный run: %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID+"/snapshots/not-a-uuid/diff?against="+first.ID, "teacher", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("невалидный head: %d %s", rec.Code, rec.Body.String())
	}

	// Снимок чужого исследования — 409, а не 404.
	otherNodes := []domain.Node{{ID: snapshotDiffTestUUID(t, pool), Type: domain.NodeRole, Title: "Чужая роль",
		Evidence: []domain.Evidence{{ID: snapshotDiffTestUUID(t, pool), SourceID: source.ID, RefID: "msg-9", Quote: "Чужая цитата"}}}}
	if err = p.SaveTree(ctx, otherRun.ID, otherNodes); err != nil {
		t.Fatal(err)
	}
	foreign := snapshotDiffCreate(t, handler, otherRun.ID)
	rec = do(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID+"/snapshots/"+second.ID+"/diff?against="+foreign.ID, "teacher", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("снимок другого run: %d %s", rec.Code, rec.Body.String())
	}
	var conflict errorBody
	if err = json.Unmarshal(rec.Body.Bytes(), &conflict); err != nil || conflict.Code != "SNAPSHOT_RUN_MISMATCH" {
		t.Fatalf("код конфликта: %+v (%v)", conflict, err)
	}

	// Чтение снимков доступно guest; student в матрице снимков не входит.
	if rec = do(t, handler, http.MethodGet, path, "guest", ""); rec.Code != http.StatusOK {
		t.Fatalf("guest: %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(t, handler, http.MethodGet, path, "student", ""); rec.Code != http.StatusOK {
		t.Fatalf("student: %d", rec.Code)
	}
	if rec = do(t, handler, http.MethodGet, path, "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("без токена: %d", rec.Code)
	}
}
