package database_test

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

	"activity-descriptor/internal/outbox"
	"activity-descriptor/internal/webhook"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTriggerAndSignedDelivery(t *testing.T) {
	url := os.Getenv("OUTBOX_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set OUTBOX_TEST_DATABASE_URL for PostgreSQL integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var runID string
	if err := pool.QueryRow(ctx, `INSERT INTO runs (profession,status) VALUES ('test','running') RETURNING id::text`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM runs WHERE id=$1::uuid`, runID)
	if _, err := pool.Exec(ctx, `UPDATE runs SET status='done' WHERE id=$1::uuid`, runID); err != nil {
		t.Fatal(err)
	}
	secret := []byte("local-test-secret")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		mac := hmac.New(sha256.New, secret)
		mac.Write(body)
		if r.Header.Get(webhook.SignatureHeader) != hex.EncodeToString(mac.Sum(nil)) {
			t.Error("bad signature")
		}
		var event struct {
			Event string `json:"event"`
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal(body, &event); err != nil || event.Event != "run.completed" || event.RunID != runID {
			t.Errorf("bad event: %s (%v)", body, err)
		}
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	sender, err := webhook.NewSender(server.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	worker := outbox.Worker{Pool: pool, Sender: sender, RetryDelay: time.Minute}
	if found, err := worker.DeliverOne(ctx); !found || err == nil {
		t.Fatalf("first attempt: found=%v err=%v", found, err)
	}
	var status string
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT status, attempts FROM agent_messages WHERE run_id=$1::uuid`, runID).Scan(&status, &attempts); err != nil || status != "failed" || attempts != 1 {
		t.Fatalf("failed state: %s %d %v", status, attempts, err)
	}
	if found, err := worker.DeliverOne(ctx); found || err != nil {
		t.Fatalf("retry should wait: found=%v err=%v", found, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_messages SET available_at=now() WHERE run_id=$1::uuid`, runID); err != nil {
		t.Fatal(err)
	}
	if found, err := worker.DeliverOne(ctx); !found || err != nil {
		t.Fatalf("second attempt: found=%v err=%v", found, err)
	}
	if err := pool.QueryRow(ctx, `SELECT status, attempts FROM agent_messages WHERE run_id=$1::uuid AND sent_at IS NOT NULL`, runID).Scan(&status, &attempts); err != nil || status != "sent" || attempts != 2 || calls != 2 {
		t.Fatalf("sent state: %s %d calls=%d err=%v", status, attempts, calls, err)
	}
	if found, err := worker.DeliverOne(ctx); found || err != nil {
		t.Fatalf("sent event redelivered: found=%v err=%v", found, err)
	}
}
