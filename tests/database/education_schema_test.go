package database_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestСтруктураПрограммИТаксономий(t *testing.T) {
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
	var source, taxonomy, other, parent, child, program, competence, ksa string
	insert := func(sql string, dest *string, args ...any) {
		t.Helper()
		if err := tx.QueryRow(ctx, sql, args...).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	insert("INSERT INTO sources(type,title) VALUES('document','тестовый документ') RETURNING id", &source)
	insert("INSERT INTO taxonomies(source_id,name) VALUES($1,'таксономия') RETURNING id", &taxonomy, source)
	insert("INSERT INTO taxonomies(name) VALUES('другая таксономия') RETURNING id", &other)
	insert("INSERT INTO taxonomy_items(taxonomy_id,code,title) VALUES($1,'A','родитель') RETURNING id", &parent, taxonomy)
	insert("INSERT INTO taxonomy_items(taxonomy_id,parent_id,code,title) VALUES($1,$2,'B','элемент') RETURNING id", &child, taxonomy, parent)
	insert("INSERT INTO education_programs(title) VALUES('программа') RETURNING id", &program)
	if _, err = tx.Exec(ctx, "INSERT INTO program_sources VALUES($1,$2)", program, source); err != nil {
		t.Fatal(err)
	}
	insert("INSERT INTO program_competencies(program_id,code,title) VALUES($1,'C','компетенция') RETURNING id", &competence, program)
	insert("INSERT INTO competency_ksa(competency_id,kind,title) VALUES($1,'knowledge','знание') RETURNING id", &ksa, competence)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO competency_taxonomy_links VALUES($1,$2)", []any{competence, child}},
		{"INSERT INTO ksa_taxonomy_links VALUES($1,$2)", []any{ksa, child}},
	} {
		if _, err = tx.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	reject := func(sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, "SAVEPOINT expected_error"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, sql, args...); err == nil {
			t.Fatalf("ожидалась ошибка для %s", sql)
		}
		if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT expected_error"); err != nil {
			t.Fatal(err)
		}
	}
	reject("INSERT INTO taxonomy_items(taxonomy_id,parent_id,title) VALUES($1,$2,'чужой родитель')", other, parent)
	reject("INSERT INTO taxonomy_items(taxonomy_id,code,title) VALUES($1,'A','дубль')", taxonomy)
	reject("INSERT INTO competency_ksa(competency_id,kind,title) VALUES($1,'unknown','ошибка')", competence)
	reject("INSERT INTO competency_taxonomy_links VALUES($1,$2)", competence, child)
	reject("DELETE FROM sources WHERE id=$1", source)
	reject("DELETE FROM taxonomy_items WHERE id=$1", child)
	var unmatched int
	insert("INSERT INTO program_competencies(program_id,title) VALUES($1,'без сопоставления') RETURNING id", &competence, program)
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM program_competencies c WHERE c.program_id=$1 AND NOT EXISTS (SELECT 1 FROM competency_taxonomy_links l WHERE l.competency_id=c.id)", program).Scan(&unmatched); err != nil || unmatched != 1 {
		t.Fatalf("несопоставленные компетенции: %d, ошибка: %v", unmatched, err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM education_programs WHERE id=$1", program); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM taxonomies WHERE id=$1", taxonomy); err != nil {
		t.Fatal(err)
	}
}
