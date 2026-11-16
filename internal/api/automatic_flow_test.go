package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Проверяем настоящий запуск этапов через API и запись дерева в отдельную БД.
func TestАвтоматическоеИсследованиеСохраняетПодтверждения(t *testing.T) {
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая БД")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	profession := fmt.Sprintf("тест-разработчик-%d", time.Now().UnixNano())
	description := "Разрабатывает на Go и Python, использует PostgreSQL."
	var source string
	if err = pool.QueryRow(ctx, `INSERT INTO sources(type,title,content,provider,external_id) VALUES('vacancy',$1,$2,'hh',$1) RETURNING id::text`, profession, description).Scan(&source); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(context.Background(), `DELETE FROM sources WHERE id=$1`, source)
	handler := NewServer(NewPGStore(pool)).Routes()
	created := do(t, handler, "POST", "/api/v1/runs", "teacher", fmt.Sprintf(`{"profession":%q}`, profession))
	if created.Code != 201 {
		t.Fatalf("%d %s", created.Code, created.Body.String())
	}
	var run domain.Run
	if err = json.Unmarshal(created.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(context.Background(), `DELETE FROM runs WHERE id=$1`, run.ID)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := NewPGStore(pool).GetRun(run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Status == domain.RunFailed {
			t.Fatal("автоматическое исследование завершилось ошибкой")
		}
		if loaded.Status == domain.RunDone {
			nodes, err := NewPGStore(pool).GetTree(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err = domain.ValidateTree(nodes); err != nil {
				t.Fatal(err)
			}
			for _, n := range nodes {
				for _, e := range n.Evidence {
					if e.SourceID != source || !strings.Contains(description, e.Quote) {
						t.Fatalf("подтверждение не из вакансии: %+v", e)
					}
				}
			}
			var linked, events int
			if err = pool.QueryRow(ctx, `SELECT count(*) FROM run_sources WHERE run_id=$1 AND source_id=$2`, run.ID, source).Scan(&linked); err != nil || linked != 1 {
				t.Fatalf("связь: %d %v", linked, err)
			}
			if err = pool.QueryRow(ctx, `SELECT count(*) FROM agent_messages WHERE run_id=$1 AND message_type='run.completed'`, run.ID).Scan(&events); err != nil || events != 1 {
				t.Fatalf("события: %d %v", events, err)
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("исследование не завершилось за 15 секунд")
}
