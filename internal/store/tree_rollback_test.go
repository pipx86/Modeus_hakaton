package store

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestОткатДереваПриОшибкеБазы(t *testing.T) {
	url := os.Getenv("STORE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("для проверки нужна STORE_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var run, source pgtype.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('проверка отката') RETURNING id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO sources(type,title) VALUES('document','источник') RETURNING id`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, run)
		_, _ = pool.Exec(ctx, `DELETE FROM sources WHERE id=$1`, source)
	}()
	newID := func() pgtype.UUID {
		var id pgtype.UUID
		if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	missingSource := newID()
	root := TreeNode{ID: newID(), Title: "роль", Evidence: []TreeEvidence{{ID: newID(), SourceID: source, Quote: "цитата"}}, Children: []TreeNode{{ID: newID(), Title: "функция", Evidence: []TreeEvidence{{ID: newID(), SourceID: missingSource, Quote: "другая цитата"}}}}}
	s := New(pool)
	if err := s.SaveTree(ctx, run, root); err == nil {
		t.Fatal("ожидалась ошибка несуществующего источника")
	}
	nodes, err := s.ListNodesByRun(ctx, run)
	if err != nil || len(nodes) != 0 {
		t.Fatalf("дерево не откатилось: %v %v", nodes, err)
	}
	var rootID pgtype.UUID
	if err = pool.QueryRow(ctx, `SELECT root_node_id FROM runs WHERE id=$1`, run).Scan(&rootID); err != nil || rootID.Valid {
		t.Fatalf("корень не откатился: %v %v", rootID, err)
	}
}
