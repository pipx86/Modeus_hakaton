package api

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"activity-descriptor/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestСнимкиЧерезAPI(t *testing.T) {
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая БД")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	p := NewPGStore(pool)
	source, err := p.CreateSource(domain.Source{Type: domain.SourceDocument, Title: "Тестовый источник", Content: "Первая цитата"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.CreateRun("Тест снимка")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, run.ID)
		pool.Exec(ctx, `DELETE FROM sources WHERE id=$1`, source.ID)
	}()
	uuid := func() string {
		var id string
		if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	nodeID, evidenceID := uuid(), uuid()
	handler := NewServer(p).Routes()
	path := "/api/v1/runs/" + run.ID + "/snapshots"
	if response := do(t, handler, "POST", path, "teacher", ""); response.Code != 409 {
		t.Fatalf("пустое дерево: %d %s", response.Code, response.Body.String())
	}
	nodes := []domain.Node{{ID: nodeID, Type: domain.NodeRole, Title: "Роль", Evidence: []domain.Evidence{{ID: evidenceID, SourceID: source.ID, Quote: "Первая цитата"}}}}
	if err = p.SaveTree(ctx, run.ID, nodes); err != nil {
		t.Fatal(err)
	}
	first := do(t, handler, "POST", path, "teacher", "")
	if first.Code != 201 {
		t.Fatalf("%d %s", first.Code, first.Body.String())
	}
	var snapshot struct {
		ID      string          `json:"id"`
		Version int             `json:"version"`
		Tree    json.RawMessage `json:"tree"`
	}
	if err = json.Unmarshal(first.Body.Bytes(), &snapshot); err != nil || snapshot.Version != 1 || !strings.Contains(string(snapshot.Tree), "Первая цитата") {
		t.Fatalf("%s %v", first.Body.String(), err)
	}
	if _, err = pool.Exec(ctx, `UPDATE evidence SET quote='Другая цитата' WHERE id=$1`, evidenceID); err != nil {
		t.Fatal(err)
	}
	second := do(t, handler, "POST", path, "teacher", "")
	if second.Code != 201 || !strings.Contains(second.Body.String(), `"version":2`) {
		t.Fatalf("%d %s", second.Code, second.Body.String())
	}
	previous := do(t, handler, "GET", path+"/"+snapshot.ID, "teacher", "")
	if previous.Code != 200 || !strings.Contains(previous.Body.String(), "Первая цитата") {
		t.Fatalf("%d %s", previous.Code, previous.Body.String())
	}
	list := do(t, handler, "GET", path, "teacher", "")
	var items []json.RawMessage
	if list.Code != 200 || json.Unmarshal(list.Body.Bytes(), &items) != nil || len(items) != 2 {
		t.Fatalf("%d %s", list.Code, list.Body.String())
	}
	if denied := do(t, handler, "POST", path, "student", ""); denied.Code != 403 {
		t.Fatalf("роль студента: %d", denied.Code)
	}
}
