package database_test

import (
	"context"
	"os"
	"testing"

	"activity-descriptor/internal/demofill"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDemoFixtureIdempotent(t *testing.T) {
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
	var occupied bool
	if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE profession=$1) OR EXISTS(SELECT 1 FROM sources WHERE title=$2)`, "ДЕМО: аналитик данных", "ДЕМО: синтетический источник, не реальный документ").Scan(&occupied); err != nil {
		t.Fatal(err)
	}
	if occupied {
		t.Skip("демоданные уже существуют: тест не изменяет их")
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM runs WHERE profession=$1`, "ДЕМО: аналитик данных")
		_, _ = pool.Exec(ctx, `DELETE FROM sources WHERE title=$1`, "ДЕМО: синтетический источник, не реальный документ")
	}()
	created, err := demofill.Apply(ctx, pool)
	if err != nil || !created {
		t.Fatalf("первое создание: создано=%v ошибка=%v", created, err)
	}
	created, err = demofill.Apply(ctx, pool)
	if err != nil || created {
		t.Fatalf("повторное создание: создано=%v ошибка=%v", created, err)
	}
	var runID string
	if err = pool.QueryRow(ctx, `SELECT id::text FROM runs WHERE profession=$1`, "ДЕМО: аналитик данных").Scan(&runID); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT type,count(*) FROM nodes WHERE run_id=$1::uuid GROUP BY type`, runID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for rows.Next() {
		var kind string
		var count int
		if err = rows.Scan(&kind, &count); err != nil {
			t.Fatal(err)
		}
		counts[kind] = count
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if counts["role"] != 1 || counts["function"] != 3 || counts["task"] != 12 || counts["action"] != 36 {
		t.Fatalf("неверный состав дерева: %v", counts)
	}
	var evidence int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM evidence e JOIN nodes n ON n.id=e.node_id WHERE n.run_id=$1::uuid`, runID).Scan(&evidence); err != nil || evidence != 52 {
		t.Fatalf("подтверждений: %d, ошибка %v", evidence, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE nodes SET title='изменено пользователем' WHERE run_id=$1::uuid AND type='role'`, runID); err != nil {
		t.Fatal(err)
	}
	if _, err = demofill.Apply(ctx, pool); err == nil {
		t.Fatal("изменённые демоданные нельзя перезаписывать")
	}
	var title string
	if err = pool.QueryRow(ctx, `SELECT title FROM nodes WHERE run_id=$1::uuid AND type='role'`, runID).Scan(&title); err != nil || title != "изменено пользователем" {
		t.Fatalf("изменения пользователя потеряны: %s %v", title, err)
	}
}

func TestDemoFixtureWithoutDatabase(t *testing.T) {
	if _, err := demofill.Apply(context.Background(), nil); err == nil {
		t.Fatal("пустое подключение не отклонено")
	}
}
