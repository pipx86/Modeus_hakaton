package database_test

import (
	"activity-descriptor/internal/store"
	"context"
	"os"
	"testing"

	"activity-descriptor/internal/store/db"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func uuid(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestStorePostgres(t *testing.T) {
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
	// The test uses a transaction for fixtures so it leaves the test database unchanged.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var runID, sourceID pgtype.UUID
	err = tx.QueryRow(ctx, "INSERT INTO runs (profession) VALUES ('store test') RETURNING id").Scan(&runID)
	if err != nil {
		t.Fatal(err)
	}
	err = tx.QueryRow(ctx, "INSERT INTO sources (run_id,type,title) VALUES ($1,'document','test') RETURNING id", runID).Scan(&sourceID)
	if err != nil {
		t.Fatal(err)
	}
	// SaveTree opens its own transaction, so commit fixtures and explicitly remove them afterwards.
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, "DELETE FROM sources WHERE id=$1", sourceID)
	defer pool.Exec(ctx, "DELETE FROM runs WHERE id=$1", runID)
	s := store.New(pool)
	additionalID := uuid(t, "00000000-0000-4000-8000-000000000004")
	if _, err := s.CreateSource(ctx, db.CreateSourceParams{ID: additionalID, RunID: runID, Type: "document", Title: "created", ProcessingStatus: "pending"}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DeleteSource(ctx, additionalID); err != nil || n != 1 {
		t.Fatalf("delete created source: %d %v", n, err)
	}
	rootID := uuid(t, "00000000-0000-4000-8000-000000000001")
	evidenceID := uuid(t, "00000000-0000-4000-8000-000000000002")
	root := store.TreeNode{ID: rootID, Title: "role", Evidence: []store.TreeEvidence{{ID: evidenceID, SourceID: sourceID, Quote: "proof"}}}
	// Дерево с некорректным дочерним узлом должно быть отклонено целиком.
	bad := root
	bad.Children = []store.TreeNode{{ID: uuid(t, "00000000-0000-4000-8000-000000000003"), Title: "child"}}
	if err := s.SaveTree(ctx, runID, bad); err == nil {
		t.Fatal("expected invalid tree to fail")
	}
	nodes, err := s.ListNodesByRun(ctx, runID)
	if err != nil || len(nodes) != 0 {
		t.Fatalf("rollback: nodes=%v, err=%v", nodes, err)
	}
	if err := s.SaveTree(ctx, runID, root); err != nil {
		t.Fatal(err)
	}
	node, err := s.GetNode(ctx, rootID)
	if err != nil || node.Title != "role" {
		t.Fatalf("get node: %v %v", node, err)
	}
	if _, err := s.UpdateNode(ctx, db.UpdateNodeParams{ID: rootID, Type: "role", Title: "updated"}); err != nil {
		t.Fatal(err)
	}
	evidence, err := s.GetEvidence(ctx, evidenceID)
	if err != nil || evidence.Quote != "proof" {
		t.Fatalf("get evidence: %v %v", evidence, err)
	}
	if _, err := s.UpdateEvidence(ctx, db.UpdateEvidenceParams{ID: evidenceID, SourceID: sourceID, Quote: "updated", Approved: true}); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.ListEvidenceByNode(ctx, rootID); err != nil || len(rows) != 1 {
		t.Fatalf("list evidence: %v %v", rows, err)
	}
	source, err := s.GetSource(ctx, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSource(ctx, db.UpdateSourceParams{ID: source.ID, RunID: source.RunID, Type: source.Type, Title: "updated", ProcessingStatus: source.ProcessingStatus}); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.ListSourcesByRun(ctx, runID); err != nil || len(rows) != 1 {
		t.Fatalf("list sources: %v %v", rows, err)
	}
	if err := s.SaveTree(ctx, runID, root); err == nil {
		t.Fatal("expected duplicate tree to fail")
	}
	if _, err := s.DeleteEvidence(ctx, evidenceID); err == nil {
		t.Fatal("удаление последнего подтверждения должно отклоняться")
	}
	if n, err := s.DeleteNode(ctx, rootID); err != nil || n != 1 {
		t.Fatalf("delete node: %d %v", n, err)
	}
	if n, err := s.DeleteSource(ctx, sourceID); err != nil || n != 1 {
		t.Fatalf("delete source: %d %v", n, err)
	}
}
