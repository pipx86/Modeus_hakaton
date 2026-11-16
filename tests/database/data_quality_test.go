package database_test

import (
	"context"
	"os"
	"testing"

	"activity-descriptor/internal/store/db"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestПроверкаКачестваДерева(t *testing.T) {
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
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var run, root, duplicate, first, second, blank pgtype.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('тест качества') RETURNING id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO nodes(run_id,type,title) VALUES($1,'role','роль') RETURNING id`, run).Scan(&root); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO nodes(run_id,type,title) VALUES($1,'role','роль') RETURNING id`, run).Scan(&duplicate); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE runs SET root_node_id=$2 WHERE id=$1`, run, root); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO nodes(run_id,type,title) VALUES($1,'function','цикл 1') RETURNING id`, run).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO nodes(run_id,type,title) VALUES($1,'task','цикл 2') RETURNING id`, run).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE nodes SET parent_id=$2 WHERE id=$1`, first, second); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE nodes SET parent_id=$2 WHERE id=$1`, second, first); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO nodes(run_id,type,title) VALUES($1,'action',E'\t') RETURNING id`, run).Scan(&blank); err != nil {
		t.Fatal(err)
	}
	issues, err := db.New(tx).ListTreeDataIssues(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]map[pgtype.UUID]bool{}
	for _, issue := range issues {
		if issue.RunID != run {
			continue
		}
		if found[issue.Code] == nil {
			found[issue.Code] = map[pgtype.UUID]bool{}
		}
		found[issue.Code][issue.NodeID] = true
	}
	for _, pair := range []struct {
		code string
		node pgtype.UUID
	}{
		{"DUPLICATE_SIBLING_TITLE", root}, {"DUPLICATE_SIBLING_TITLE", duplicate},
		{"ORPHAN_NODE", duplicate}, {"EMPTY_TITLE", blank}, {"MISSING_EVIDENCE", root}, {"MISSING_EVIDENCE", first},
		{"CYCLE_DETECTED", first}, {"CYCLE_DETECTED", second},
	} {
		if !found[pair.code][pair.node] {
			t.Errorf("не найдена проблема %s для узла %v", pair.code, pair.node)
		}
	}
}
