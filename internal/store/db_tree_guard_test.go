package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestБазаОтклоняетНекорректноеДерево(t *testing.T) {
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
	if err = pool.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('проверка ограничений') RETURNING id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO sources(type,title) VALUES('document','источник для проверки') RETURNING id`).Scan(&source); err != nil {
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
	root, first, second, grandchild := newID(), newID(), newID(), newID()
	node := func(id pgtype.UUID, title string, children ...TreeNode) TreeNode {
		return TreeNode{ID: id, Title: title, Evidence: []TreeEvidence{{ID: newID(), SourceID: source, Quote: "подтверждение"}}, Children: children}
	}
	tree := node(root, "роль", node(first, "первая функция", node(grandchild, "задача")), node(second, "вторая функция"))
	if err = New(pool).SaveTree(ctx, run, tree); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		sql  string
		args []any
	}{
		{"пустое название", `UPDATE nodes SET title=E'\t' WHERE id=$1`, []any{first}},
		{"дубль у родителя", `UPDATE nodes SET title=' первая функция ' WHERE id=$1`, []any{second}},
		{"узел без родителя", `UPDATE nodes SET parent_id=NULL WHERE id=$1`, []any{first}},
		{"цикл", `UPDATE nodes SET parent_id=$2 WHERE id=$1`, []any{first, grandchild}},
		{"без подтверждения", `DELETE FROM evidence WHERE node_id=$1`, []any{first}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err = tx.Exec(ctx, tc.sql, tc.args...); err == nil {
				err = tx.Commit(ctx)
			}
			if err == nil {
				t.Fatalf("база приняла проблему: %s", tc.name)
			}
			codes := map[string]string{"пустое название": "EMPTY_TITLE", "дубль у родителя": "DUPLICATE_SIBLING_TITLE", "узел без родителя": "ORPHAN_NODE", "цикл": "CYCLE_DETECTED", "без подтверждения": "MISSING_EVIDENCE"}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.ConstraintName != codes[tc.name] {
				t.Fatalf("нестабильный код ошибки для %s: %v", tc.name, err)
			}
		})
	}
}

func TestПоследниеПодтвержденияПриПараллельномУдалении(t *testing.T) {
	url := os.Getenv("STORE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("для проверки нужна STORE_TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var run, source, node, first, second pgtype.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('параллельная проверка') RETURNING id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO sources(type,title) VALUES('document','источник') RETURNING id`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM runs WHERE id=$1`, run)
		_, _ = pool.Exec(context.Background(), `DELETE FROM sources WHERE id=$1`, source)
	}()
	newID := func() pgtype.UUID {
		var id pgtype.UUID
		if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	node, first, second = newID(), newID(), newID()
	root := TreeNode{ID: node, Title: "роль", Evidence: []TreeEvidence{{ID: first, SourceID: source, Quote: "первая"}, {ID: second, SourceID: source, Quote: "вторая"}}}
	if err = New(pool).SaveTree(ctx, run, root); err != nil {
		t.Fatal(err)
	}
	tx1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx1.Rollback(context.Background())
	if _, err = tx1.Exec(ctx, `DELETE FROM evidence WHERE id=$1`, first); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		tx, err := pool.Begin(ctx)
		if err != nil {
			result <- err
			return
		}
		defer tx.Rollback(context.Background())
		if _, err = tx.Exec(ctx, `DELETE FROM evidence WHERE id=$1`, second); err == nil {
			err = tx.Commit(ctx)
		}
		result <- err
	}()
	if err = tx1.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		if err == nil {
			t.Fatal("оба последних подтверждения были удалены")
		}
	case <-ctx.Done():
		t.Fatal(fmt.Errorf("параллельная проверка не завершилась: %w", ctx.Err()))
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM evidence WHERE node_id=$1`, node).Scan(&count); err != nil || count != 1 {
		t.Fatalf("осталось подтверждений: %d, ошибка: %v", count, err)
	}
}
