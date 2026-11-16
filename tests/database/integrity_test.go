package database_test

import (
	"activity-descriptor/internal/store"
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestРодительВДругомИсследовании(t *testing.T) {
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
	var first, second, source pgtype.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('первое') RETURNING id`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('второе') RETURNING id`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO sources(type,title) VALUES('document','источник') RETURNING id`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM runs WHERE id=$1 OR id=$2`, first, second)
		_, _ = pool.Exec(ctx, `DELETE FROM sources WHERE id=$1`, source)
	}()
	newID := func() pgtype.UUID {
		var id pgtype.UUID
		if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	root, child := newID(), newID()
	tree := store.TreeNode{ID: root, Title: "первая роль", Evidence: []store.TreeEvidence{{ID: newID(), SourceID: source, Quote: "цитата"}}, Children: []store.TreeNode{{ID: child, Title: "своя функция", Evidence: []store.TreeEvidence{{ID: newID(), SourceID: source, Quote: "цитата"}}}}}
	if err = store.New(pool).SaveTree(ctx, first, tree); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO nodes(run_id,parent_id,type,title) VALUES($1,$2,'function','чужая функция')`, second, root); err == nil {
		t.Fatal("база приняла родителя из другого исследования")
	}
	if _, err = pool.Exec(ctx, `UPDATE runs SET root_node_id=$2 WHERE id=$1`, second, root); err == nil {
		t.Fatal("база приняла корень из другого исследования")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM nodes WHERE id=$1`, root); err != nil {
		t.Fatal(err)
	}
	var savedRoot pgtype.UUID
	if err = pool.QueryRow(ctx, `SELECT root_node_id FROM runs WHERE id=$1`, first).Scan(&savedRoot); err != nil || savedRoot.Valid {
		t.Fatalf("после удаления корень не обнулён: %v %v", savedRoot, err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM nodes WHERE id=$1`, child).Scan(&count); err != nil || count != 0 {
		t.Fatalf("каскадное удаление: %d %v", count, err)
	}
}
