package database_test

import (
	"activity-descriptor/internal/store"
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSaveMultiLevelTree(t *testing.T) {
	databaseURL := os.Getenv("STORE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set STORE_TEST_DATABASE_URL for PostgreSQL integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var runID, sourceID pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO runs (profession) VALUES ('tree integration test') RETURNING id`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, runID)
	if err := pool.QueryRow(ctx, `INSERT INTO sources(run_id,type,title) VALUES ($1,'document','test source') RETURNING id`, runID).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, runID)
		_, _ = pool.Exec(ctx, `DELETE FROM sources WHERE id=$1`, sourceID)
	}()
	newID := func() pgtype.UUID {
		var id pgtype.UUID
		if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	evidence := func() []store.TreeEvidence {
		return []store.TreeEvidence{{ID: newID(), SourceID: sourceID, Quote: "test citation"}}
	}
	roleID, functionID, taskID, actionID := newID(), newID(), newID(), newID()
	root := store.TreeNode{ID: roleID, Title: "role", Evidence: evidence(), Children: []store.TreeNode{{ID: functionID, Title: "function", Evidence: evidence(), Children: []store.TreeNode{{ID: taskID, Title: "task", Evidence: evidence(), Children: []store.TreeNode{{ID: actionID, Title: "action", Evidence: evidence()}}}}}}}
	s := store.New(pool)
	if err := s.SaveTree(ctx, runID, root); err != nil {
		t.Fatal(err)
	}
	nodes, err := s.ListNodesByRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 4 {
		t.Fatalf("expected 4 nodes, got %d", len(nodes))
	}
	ids := []pgtype.UUID{roleID, functionID, taskID, actionID}
	types := []string{"role", "function", "task", "action"}
	for i, n := range nodes {
		if n.ID != ids[i] || n.Type != types[i] || n.Level != int16(i) {
			t.Fatalf("unexpected node at level %d: %+v", i, n)
		}
		if i == 0 && n.ParentID.Valid {
			t.Fatal("root should not have a parent")
		}
		if i > 0 && n.ParentID != ids[i-1] {
			t.Fatalf("wrong parent at level %d", i)
		}
		citations, err := s.ListEvidenceByNode(ctx, n.ID)
		if err != nil || len(citations) != 1 {
			t.Fatalf("evidence at level %d: %v %v", i, citations, err)
		}
	}
	var savedRoot pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT root_node_id FROM runs WHERE id=$1`, runID).Scan(&savedRoot); err != nil || savedRoot != roleID {
		t.Fatalf("root: %v %v", savedRoot, err)
	}
}
