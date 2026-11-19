package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"activity-descriptor/internal/api"
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

// finalizeTestPool подключается к отдельной тестовой PostgreSQL, только если
// задан INGEST_TEST_DATABASE_URL; иначе тест пропускается как остальные DB-тесты.
func finalizeTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая БД (INGEST_TEST_DATABASE_URL)")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestFinalizeInterviewPostgresEndToEnd проверяет production-путь целиком на
// реальной PostgreSQL без LLM: источники run читаются из run_sources и доходят
// до синтезатора, интервью-источник и дерево сохраняются, конфликты и сообщения
// синтеза доступны через authenticated API.
func TestFinalizeInterviewPostgresEndToEnd(t *testing.T) {
	pool := finalizeTestPool(t)
	ctx := context.Background()
	store := api.NewPGStore(pool)

	run, err := store.CreateRun("Аналитик данных")
	if err != nil {
		t.Fatal(err)
	}
	var vacancySourceID string
	if err = pool.QueryRow(ctx, `INSERT INTO sources(type,title,content,run_id) VALUES('vacancy','Аналитик данных (hh)','SQL, отчётность',$1::uuid) RETURNING id::text`, run.ID).Scan(&vacancySourceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM runs WHERE id=$1::uuid`, run.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM sources WHERE id=$1::uuid`, vacancySourceID)
	})

	synthesis := testSynthesisOutput()
	synth := &fakeSynthesisRunner{output: synthesis}
	status, err := finalizeInterview(ctx, finalizerDeps{
		store:       store,
		analyst:     &fakeInterviewAnalyst{output: testAnalystOutput()},
		synthesizer: synth,
	}, run.ID, "Аналитик данных", testFinalizeSession())
	if err != nil || status != domain.RunDone {
		t.Fatalf("finalize = %v, %v; want done", status, err)
	}

	// Реальный источник run передан синтезатору как метаданные.
	if len(synth.input.Sources) != 1 {
		t.Fatalf("synthesizer sources = %+v, want one linked vacancy", synth.input.Sources)
	}
	if got := synth.input.Sources[0]; got.ID != vacancySourceID || got.Type != domain.SourceVacancy || got.Title != "Аналитик данных (hh)" {
		t.Fatalf("synthesizer source = %+v, want linked vacancy %s", got, vacancySourceID)
	}
	if len(synth.input.Vacancy) != 0 || len(synth.input.Standard) != 0 {
		t.Fatalf("finalizer fabricated nodes: standard=%+v vacancy=%+v", synth.input.Standard, synth.input.Vacancy)
	}

	// Run завершён, дерево и интервью-источник сохранены.
	var runStatus, runStage string
	var rootNodeID *string
	if err = pool.QueryRow(ctx, `SELECT status::text, stage::text, root_node_id::text FROM runs WHERE id=$1::uuid`, run.ID).Scan(&runStatus, &runStage, &rootNodeID); err != nil {
		t.Fatal(err)
	}
	if runStatus != "done" || runStage != "done" || rootNodeID == nil {
		t.Fatalf("run status=%q stage=%q root=%v", runStatus, runStage, rootNodeID)
	}
	var nodeCount, evidenceCount, linkedSources int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM nodes WHERE run_id=$1::uuid`, run.ID).Scan(&nodeCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM evidence WHERE run_id=$1::uuid`, run.ID).Scan(&evidenceCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM run_sources WHERE run_id=$1::uuid`, run.ID).Scan(&linkedSources); err != nil {
		t.Fatal(err)
	}
	if nodeCount != len(testInterviewNodes()) || evidenceCount != len(testInterviewNodes()) || linkedSources != 2 {
		t.Fatalf("nodes=%d evidence=%d linked sources=%d", nodeCount, evidenceCount, linkedSources)
	}
	var snapshots int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM snapshots WHERE run_id=$1::uuid`, run.ID).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if snapshots < 1 {
		t.Fatal("automatic snapshot was not saved")
	}

	// Конфликты/сообщения синтеза сохранены в agent_messages с run_id.
	var reviewRows int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM agent_messages WHERE run_id=$1::uuid AND message_type IN ('synthesis.conflict','synthesis.message')`, run.ID).Scan(&reviewRows); err != nil {
		t.Fatal(err)
	}
	if reviewRows != len(synthesis.Conflicts)+len(synthesis.Messages) {
		t.Fatalf("agent_messages review rows = %d, want %d", reviewRows, len(synthesis.Conflicts)+len(synthesis.Messages))
	}

	// Authenticated read API отдаёт те же конфликты.
	handler := api.NewServer(store).Routes()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/runs/"+run.ID+"/conflicts", nil)
	request.Header.Set("Authorization", "Bearer "+auth.TokenFor("reviewer", auth.RoleAdmin))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET conflicts = %d %s", recorder.Code, recorder.Body.String())
	}
	var gotConflicts []domain.Conflict
	if err = json.Unmarshal(recorder.Body.Bytes(), &gotConflicts); err != nil {
		t.Fatalf("conflicts JSON: %v (%s)", err, recorder.Body.String())
	}
	if len(gotConflicts) != len(synthesis.Conflicts) || gotConflicts[0].ID != synthesis.Conflicts[0].ID {
		t.Fatalf("API conflicts = %+v, want %+v", gotConflicts, synthesis.Conflicts)
	}
}
