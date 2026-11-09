package demofill

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestПовторноеСозданиеПримера(t *testing.T) {
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
	run, source := identifier("run"), identifier("source")
	var occupied bool
	if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE id=$1) OR EXISTS(SELECT 1 FROM sources WHERE id=$2)`, run, source).Scan(&occupied); err != nil {
		t.Fatal(err)
	}
	if occupied {
		t.Skip("идентификаторы демо уже заняты: тест не изменяет существующие данные")
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, run)
		_, _ = pool.Exec(ctx, `DELETE FROM sources WHERE id=$1`, source)
	}()
	created, err := Apply(ctx, pool)
	if err != nil || !created {
		t.Fatalf("первое создание: %v %v", created, err)
	}
	created, err = Apply(ctx, pool)
	if err != nil || created {
		t.Fatalf("повторное создание: %v %v", created, err)
	}
	rows, err := pool.Query(ctx, `SELECT type,count(*) FROM nodes WHERE run_id=$1 GROUP BY type`, run)
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
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM evidence e JOIN nodes n ON n.id=e.node_id WHERE n.run_id=$1`, run).Scan(&evidence); err != nil || evidence != 52 {
		t.Fatalf("подтверждений: %d, ошибка %v", evidence, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE nodes SET title='изменено пользователем' WHERE id=$1`, identifier("node/role")); err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(ctx, pool); err == nil {
		t.Fatal("изменённые демоданные нельзя перезаписывать")
	}
	var title string
	if err = pool.QueryRow(ctx, `SELECT title FROM nodes WHERE id=$1`, identifier("node/role")).Scan(&title); err != nil || title != "изменено пользователем" {
		t.Fatalf("изменения пользователя потеряны: %s %v", title, err)
	}
}
