package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"activity-descriptor/internal/agent/synthesizer"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/steps"

	"github.com/jackc/pgx/v5/pgxpool"
)

// integrationAnalystClient returns one evidence-backed role entity. The quote is
// a verbatim fragment of the transcript used by the integration test below.
type integrationAnalystClient struct{}

func (integrationAnalystClient) Chat(context.Context, []llm.Message) (string, error) {
	return `{"entities":[{"id":"role-1","type":"role","title":"Аналитик данных","evidence":[{"id":"e1","source_id":"interview-transcript","ref_id":"q-1","quote":"готовлю отчёты"}]}]}`, nil
}

// TestBatchTranscriptRunPersistsTree exercises the real create-run HTTP handler
// with an AI batch plan: the transcript from the request is persisted as an
// interview source, the analyst/synthesizer chain runs through
// domain.Orchestrate and the UUID-repaired tree is written by PGStore.
//
// It needs a disposable PostgreSQL database and is skipped otherwise.
func TestBatchTranscriptRunPersistsTree(t *testing.T) {
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

	pgStore := NewPGStore(pool)
	synth, err := synthesizer.New()
	if err != nil {
		t.Fatalf("synthesizer.New: %v", err)
	}
	server := NewServer(pgStore)
	server.SetBatchPlan(steps.NewBatchPlan(steps.BatchOptions{
		Analyst:     integrationAnalystClient{},
		Synthesizer: synth,
		Store:       pgStore,
		VacancyTopK: 10,
	}))
	handler := server.Routes()

	profession := fmt.Sprintf("интеграционный аналитик %d", time.Now().UnixNano())
	transcript := "Я аналитик данных и готовлю отчёты."
	body := fmt.Sprintf(`{"profession":%q,"transcript":%q}`, profession, transcript)
	created := do(t, handler, "POST", "/api/v1/runs", "teacher", body)
	if created.Code != 201 {
		t.Fatalf("%d %s", created.Code, created.Body.String())
	}
	var run domain.Run
	if err = json.Unmarshal(created.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	defer func() {
		pool.Exec(context.Background(), "DELETE FROM sources WHERE run_id=$1", run.ID)
		pool.Exec(context.Background(), "DELETE FROM runs WHERE id=$1", run.ID)
	}()

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		loaded, getErr := pgStore.GetRun(run.ID)
		if getErr != nil {
			t.Fatalf("get run: %v", getErr)
		}
		if loaded.Status == domain.RunFailed {
			t.Fatalf("AI batch run failed: stage=%s", loaded.Stage)
		}
		if loaded.Status == domain.RunDone {
			nodes, treeErr := pgStore.GetTree(run.ID)
			if treeErr != nil {
				t.Fatalf("get tree: %v", treeErr)
			}
			if len(nodes) == 0 {
				t.Fatal("AI batch run finished without a tree")
			}
			if err = domain.ValidateTree(nodes); err != nil {
				t.Fatalf("persisted tree invalid: %v", err)
			}

			var sourceID string
			if err = pool.QueryRow(ctx, `SELECT s.id::text FROM sources s
JOIN run_sources rs ON rs.source_id=s.id
WHERE rs.run_id=$1 AND s.type='interview'`, run.ID).Scan(&sourceID); err != nil {
				t.Fatalf("interview source not linked to the run: %v", err)
			}
			for _, node := range nodes {
				for _, evidence := range node.Evidence {
					if evidence.SourceID != sourceID {
						t.Fatalf("evidence source = %q, want interview source %q", evidence.SourceID, sourceID)
					}
				}
			}
			var sourceCount int
			if err = pool.QueryRow(ctx, `SELECT count(*) FROM sources WHERE run_id=$1`, run.ID).Scan(&sourceCount); err != nil {
				t.Fatalf("count sources: %v", err)
			}
			if sourceCount != 1 {
				t.Fatalf("sources = %d, want the single interview transcript", sourceCount)
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("AI batch run did not finish in 20 seconds")
}
