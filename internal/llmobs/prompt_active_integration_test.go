package llmobs

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestActivePromptVersionIntegration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" {
		t.Skip("TEST_DATABASE_URL/DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	var llmTables bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.prompt_versions') IS NOT NULL`).Scan(&llmTables); err != nil || !llmTables {
		t.Skip("LLM prompt tables are not migrated in this database")
	}

	id := fmt.Sprintf("integration-prompt-%d", time.Now().UnixNano())
	if _, err := pool.Exec(ctx, `
		INSERT INTO prompt_versions (id, agent, version, prompt_hash, system_text, status, activated_at)
		VALUES ($1, 'synthesizer', 'integration', repeat('c', 64), 'integration prompt', 'active', now())`, id); err != nil {
		t.Fatalf("seed active prompt: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM prompt_versions WHERE id=$1`, id)
	})

	got, err := NewStore(pool).ActivePromptVersion(ctx, "synthesizer")
	if err != nil {
		t.Fatalf("ActivePromptVersion: %v", err)
	}
	if got.ID != id || got.Version != "integration" || got.Status != "active" {
		t.Fatalf("prompt = %#v", got)
	}
}
