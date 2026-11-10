package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/outbox"
	"activity-descriptor/internal/store"
	"activity-descriptor/internal/webhook"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Этот сценарий проверяет соединение компонентов, шаги агентов заменены фикстурами.
func TestОбщийСценарийИсследования(t *testing.T) {
	if os.Getenv("MODEUS_ACCEPTANCE_EXCLUSIVE") != "1" {
		t.Skip("нужна отдельная БД и MODEUS_ACCEPTANCE_EXCLUSIVE=1")
	}
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("не задана INGEST_TEST_DATABASE_URL")
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	storage := NewPGStore(pool)
	handler := NewServer(storage).Routes()
	created := do(t, handler, "POST", "/api/v1/runs", "teacher", `{"profession":"Синтетический аналитик"}`)
	if created.Code != 201 {
		t.Fatalf("создание: %d %s", created.Code, created.Body.String())
	}
	var run domain.Run
	if err = json.Unmarshal(created.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	var source string
	if err = pool.QueryRow(ctx, "INSERT INTO sources(type,title,content) VALUES('interview','Учебное интервью','Я проверяю качество данных и описываю результаты') RETURNING id::text").Scan(&source); err != nil {
		t.Fatal(err)
	}
	defer func() {
		pool.Exec(context.Background(), "DELETE FROM runs WHERE id=$1", run.ID)
		pool.Exec(context.Background(), "DELETE FROM sources WHERE id=$1", source)
	}()
	uuid := func() string {
		var id string
		if err := pool.QueryRow(ctx, "SELECT gen_random_uuid()::text").Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	ids := []string{uuid(), uuid(), uuid(), uuid()}
	kinds := []domain.NodeType{domain.NodeRole, domain.NodeFunction, domain.NodeTask, domain.NodeAction}
	var nodes []domain.Node
	for i, kind := range kinds {
		n := domain.Node{ID: ids[i], Type: kind, Title: []string{"Аналитик", "Анализировать данные", "Проверять качество", "Проверять пропуски"}[i], Evidence: []domain.Evidence{{ID: uuid(), SourceID: source, Quote: "Я проверяю качество данных"}}}
		if i > 0 {
			parent := ids[i-1]
			n.ParentID = &parent
		}
		nodes = append(nodes, n)
	}
	notFinished := do(t, handler, "GET", "/api/v1/runs/"+run.ID+"/export", "teacher", "")
	if notFinished.Code != 409 {
		t.Fatal("экспорт до завершения разрешён")
	}
	steps := []domain.Step{
		flowStep{domain.StageInterview, func() error {
			var content string
			return pool.QueryRow(ctx, "SELECT content FROM sources WHERE id=$1", source).Scan(&content)
		}},
		flowStep{domain.StageAnalysis, func() error { return domain.ValidateTree(nodes) }},
		flowStep{domain.StageSynthesis, func() error {
			body, e := json.Marshal(nodes)
			if e != nil {
				return e
			}
			r := do(t, handler, "PUT", "/api/v1/runs/"+run.ID+"/tree", "teacher", string(body))
			if r.Code != 204 {
				t.Fatalf("синтез: %d %s", r.Code, r.Body.String())
			}
			return nil
		}},
		flowStep{domain.StageReview, func() error {
			for _, n := range nodes {
				r := do(t, handler, "POST", "/api/v1/evidences/"+n.Evidence[0].ID+"/approve", "teacher", "")
				if r.Code != 204 {
					t.Fatalf("одобрение: %d %s", r.Code, r.Body.String())
				}
			}
			return nil
		}},
	}
	if err = domain.Orchestrate(ctx, &run, steps, storage, nil, 0, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewPGStore(pool).GetRun(run.ID)
	if err != nil || loaded.Stage != domain.StageDone || loaded.Status != domain.RunDone {
		t.Fatalf("%+v %v", loaded, err)
	}
	exported := do(t, handler, "GET", "/api/v1/runs/"+run.ID+"/export", "teacher", "")
	if exported.Code != 200 {
		t.Fatalf("экспорт: %d", exported.Code)
	}
	var result []domain.Node
	if err = json.Unmarshal(exported.Body.Bytes(), &result); err != nil || len(result) != 4 {
		t.Fatalf("дерево: %d %v", len(result), err)
	}
	var runID pgtype.UUID
	runID.Scan(run.ID)
	snapshot, err := store.New(pool).SaveSnapshot(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	var frozen struct {
		Nodes []struct {
			Evidence []struct {
				Approved bool
				Quote    string
			}
		}
	}
	if err = json.Unmarshal(snapshot.Tree, &frozen); err != nil || len(frozen.Nodes) != 4 {
		t.Fatalf("снимок: %v", err)
	}
	for _, n := range frozen.Nodes {
		if len(n.Evidence) != 1 || !n.Evidence[0].Approved || n.Evidence[0].Quote == "" {
			t.Fatal("снимок не содержит одобренную цитату")
		}
	}
	secret := []byte("тестовый-секрет-получателя")
	received := make(chan string, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, e := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if e != nil {
			w.WriteHeader(400)
			return
		}
		signature, e := hex.DecodeString(r.Header.Get(webhook.SignatureHeader))
		mac := hmac.New(sha256.New, secret)
		mac.Write(body)
		if e != nil || !hmac.Equal(signature, mac.Sum(nil)) {
			w.WriteHeader(401)
			return
		}
		var event struct {
			Event string `json:"event"`
			RunID string `json:"run_id"`
		}
		if json.Unmarshal(body, &event) != nil || event.Event != "run.completed" || event.RunID != run.ID {
			w.WriteHeader(400)
			return
		}
		received <- event.RunID
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	sender, err := webhook.NewSender(receiver.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	worker := outbox.Worker{Pool: pool, Sender: sender, RetryDelay: time.Second}
	found, err := worker.DeliverOne(ctx)
	if err != nil || !found {
		t.Fatalf("доставка: %v %v", found, err)
	}
	select {
	case id := <-received:
		if id != run.ID {
			t.Fatal("получено чужое событие")
		}
	case <-ctx.Done():
		t.Fatal("событие не получено")
	}
	var status string
	var attempts int
	if err = pool.QueryRow(ctx, "SELECT status,attempts FROM agent_messages WHERE run_id=$1 AND message_type='run.completed'", run.ID).Scan(&status, &attempts); err != nil || status != "sent" || attempts != 1 {
		t.Fatalf("%s %d %v", status, attempts, err)
	}
	t.Logf("run → интервью → анализ → синтез → approve → snapshot → export → подписанный webhook: %s; агенты тестовые", time.Since(started))
}

type flowStep struct {
	stage   domain.Stage
	execute func() error
}

func (s flowStep) Name() domain.Stage                         { return s.stage }
func (s flowStep) Execute(context.Context, *domain.Run) error { return s.execute() }
