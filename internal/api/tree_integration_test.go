package api

import (
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/logging"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func init() {
	logging.Init()
}

func TestЗаписьДереваЧерезAPI(t *testing.T) {
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая PostgreSQL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	storage := NewPGStore(pool)
	run, err := storage.CreateRun("Тест постоянного хранилища")
	if err != nil {
		t.Fatal(err)
	}
	var source string
	if err = pool.QueryRow(ctx, "INSERT INTO sources(type,title) VALUES('document','тест дерева') RETURNING id::text").Scan(&source); err != nil {
		t.Fatal(err)
	}
	defer func() {
		pool.Exec(ctx, "DELETE FROM runs WHERE id=$1", run.ID)
		pool.Exec(ctx, "DELETE FROM sources WHERE id=$1", source)
	}()
	uuid := func() string {
		var id string
		if err := pool.QueryRow(ctx, "SELECT gen_random_uuid()::text").Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	root := uuid()
	nodes := []domain.Node{{ID: root, Type: domain.NodeRole, Title: "Роль", Evidence: []domain.Evidence{{ID: uuid(), SourceID: source, Quote: "цитата"}}}}
	body, _ := json.Marshal(nodes)
	handler := NewServer(storage).Routes()
	nodes[0].Title = " "
	bad, _ := json.Marshal(nodes)
	rr := do(t, handler, "PUT", "/api/v1/runs/"+run.ID+"/tree", "teacher", string(bad))
	if rr.Code != 422 {
		t.Fatalf("ожидали 422: %d %s", rr.Code, rr.Body.String())
	}
	var result map[string]any
	json.Unmarshal(rr.Body.Bytes(), &result)
	if result["code"] != "EMPTY_TITLE" || result["details"] == nil {
		t.Fatalf("неверная ошибка: %v", result)
	}
	var count int
	pool.QueryRow(ctx, "SELECT count(*) FROM nodes WHERE run_id=$1", run.ID).Scan(&count)
	if count != 0 {
		t.Fatal("невалидное дерево записалось")
	}
	rr = do(t, handler, "PUT", "/api/v1/runs/"+run.ID+"/tree", "student", string(body))
	if rr.Code != 403 {
		t.Fatalf("ожидали 403, получили %d", rr.Code)
	}
	rr = do(t, handler, "PUT", "/api/v1/runs/"+run.ID+"/tree", "teacher", string(body))
	if rr.Code != 204 {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	loaded, err := NewPGStore(pool).GetTree(run.ID)
	if err != nil || len(loaded) != 1 || len(loaded[0].Evidence) != 1 {
		t.Fatalf("%+v %v", loaded, err)
	}

	steps := []domain.Step{acceptanceStage(domain.StageInterview), acceptanceStage(domain.StageAnalysis), acceptanceStage(domain.StageSynthesis), acceptanceStage(domain.StageReview)}
	if err = domain.Orchestrate(ctx, run, steps, storage, nil, 0, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	exported := do(t, handler, "GET", "/api/v1/runs/"+run.ID+"/export", "teacher", "")
	if exported.Code != 200 {
		t.Fatalf("экспорт: %d %s", exported.Code, exported.Body.String())
	}
	loadedRun, err := NewPGStore(pool).GetRun(run.ID)
	if err != nil || loadedRun.Status != domain.RunDone || loadedRun.Stage != domain.StageDone || loadedRun.FinishedAt == nil {
		t.Fatalf("%+v %v", loadedRun, err)
	}
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM agent_messages WHERE run_id=$1 AND message_type='run.completed'", run.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("события: %d %v", count, err)
	}
	if err = storage.ApproveEvidence(loaded[0].Evidence[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = storage.ApproveEvidence(loaded[0].Evidence[0].ID); err != ErrAlreadyApproved {
		t.Fatalf("повторное одобрение: %v", err)
	}
	// Неудачная ссылка должна откатить и узлы, созданные до неё.
	second, err := storage.CreateRun("Тест отката")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, "DELETE FROM runs WHERE id=$1", second.ID)
	invalid := []domain.Node{{ID: uuid(), Type: domain.NodeRole, Title: "Другая роль", Evidence: []domain.Evidence{{ID: uuid(), SourceID: uuid(), Quote: "цитата"}}}}
	if err = storage.SaveTree(ctx, second.ID, invalid); err == nil {
		t.Fatal("несуществующий источник принят")
	}
	pool.QueryRow(ctx, "SELECT count(*) FROM nodes WHERE run_id=$1", second.ID).Scan(&count)
	if count != 0 {
		t.Fatal("транзакция не откатилась")
	}
}

type acceptanceStage domain.Stage

func (s acceptanceStage) Name() domain.Stage                         { return domain.Stage(s) }
func (s acceptanceStage) Execute(context.Context, *domain.Run) error { return nil }
