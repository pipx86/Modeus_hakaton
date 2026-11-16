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

func TestОбщийИсточникИЗащитаПодтверждений(t *testing.T) {
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
	var first, second, sourceID, nodeID pgtype.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('первое') RETURNING id`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('второе') RETURNING id`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM runs WHERE id=$1 OR id=$2`, first, second)
		_, _ = pool.Exec(ctx, `DELETE FROM sources WHERE id=$1`, sourceID)
	}()
	if err = pool.QueryRow(ctx, `INSERT INTO sources(run_id,type,title) VALUES($1,'document','общий источник') RETURNING id`, first).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	s := store.New(pool)
	firstList, err := s.ListSourcesByRun(ctx, first)
	if err != nil || len(firstList) != 1 {
		t.Fatalf("старая связь: %v %v", firstList, err)
	}
	params := db.LinkSourceToRunParams{RunID: second, SourceID: sourceID}
	if n, err := s.LinkSourceToRun(ctx, params); err != nil || n != 1 {
		t.Fatalf("новая связь: %d %v", n, err)
	}
	if n, err := s.LinkSourceToRun(ctx, params); err != nil || n != 0 {
		t.Fatalf("повторная связь: %d %v", n, err)
	}
	secondList, err := s.ListSourcesByRun(ctx, second)
	if err != nil || len(secondList) != 1 || secondList[0].ID != sourceID {
		t.Fatalf("общий источник: %v %v", secondList, err)
	}
	if err = pool.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	var evidenceID pgtype.UUID
	if err = pool.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&evidenceID); err != nil {
		t.Fatal(err)
	}
	root := store.TreeNode{ID: nodeID, Title: "роль", Evidence: []store.TreeEvidence{{ID: evidenceID, SourceID: sourceID, Quote: "цитата"}}}
	if err = s.SaveTree(ctx, first, root); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeleteSource(ctx, sourceID); err == nil {
		t.Fatal("удаление источника с подтверждением должно быть запрещено")
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM evidence WHERE source_id=$1`, sourceID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("подтверждение потеряно: %d %v", count, err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, first); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DeleteSource(ctx, sourceID); err != nil || n != 1 {
		t.Fatalf("удаление после подтверждений: %d %v", n, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM run_sources WHERE source_id=$1`, sourceID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("связи не удалены: %d %v", count, err)
	}
}

func TestАвтопривязкаИсточникаДляПодтверждения(t *testing.T) {
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
	var run, source, node pgtype.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('проверка привязки') RETURNING id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO sources(type,title) VALUES('document','независимый источник') RETURNING id`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, run)
		_, _ = pool.Exec(ctx, `DELETE FROM sources WHERE id=$1`, source)
	}()
	if err = pool.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&node); err != nil {
		t.Fatal(err)
	}
	var evidenceID pgtype.UUID
	if err = pool.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&evidenceID); err != nil {
		t.Fatal(err)
	}
	root := store.TreeNode{ID: node, Title: "роль", Evidence: []store.TreeEvidence{{ID: evidenceID, SourceID: source, Quote: "цитата"}}}
	if err = store.New(pool).SaveTree(ctx, run, root); err != nil {
		t.Fatal(err)
	}
	var links int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM run_sources WHERE run_id=$1 AND source_id=$2`, run, source).Scan(&links); err != nil || links != 1 {
		t.Fatalf("связь не создана: %d %v", links, err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM run_sources WHERE run_id=$1 AND source_id=$2`, run, source); err == nil {
		t.Fatal("удаление используемой связи должно быть запрещено")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, run); err != nil {
		t.Fatalf("исследование должно удаляться с подтверждениями: %v", err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM sources WHERE id=$1`, source); err != nil {
		t.Fatalf("неиспользуемый источник должен удаляться: %v", err)
	}
}
