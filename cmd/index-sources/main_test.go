package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func testUUID(n byte) pgtype.UUID {
	var id pgtype.UUID
	id.Bytes[15] = n
	id.Valid = true
	return id
}

func TestParseOptionsDefaultsFromEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://activity@127.0.0.1:5432/activity_descriptor?sslmode=disable")
	t.Setenv("OLLAMA_URL", "")
	t.Setenv("OLLAMA_EMBED_MODEL", "")
	opts, err := parseOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.ollamaURL != "http://127.0.0.1:11434" || opts.embedModel != "nomic-embed-text" {
		t.Fatalf("локальные умолчания Ollama: %+v", opts)
	}
	if opts.limit != 0 || opts.all || opts.dryRun || opts.allowFailed {
		t.Fatalf("неверные умолчания: %+v", opts)
	}
	if opts.timeout != defaultSourceTimeout {
		t.Fatalf("timeout: %s", opts.timeout)
	}
}

func TestParseOptionsFlags(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://activity@127.0.0.1:5432/activity_descriptor?sslmode=disable")
	t.Setenv("OLLAMA_URL", "http://127.0.0.1:11434")
	opts, err := parseOptions([]string{
		"-limit", "7",
		"-all",
		"-dry-run",
		"-allow-failures",
		"-timeout", "90s",
		"-ollama-url", "http://127.0.0.1:12345",
		"-embed-model", "test-embed",
		"-source", "11111111-1111-1111-1111-111111111111",
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.limit != 7 || !opts.all || !opts.dryRun || !opts.allowFailed {
		t.Fatalf("флаги не разобраны: %+v", opts)
	}
	if opts.timeout != 90*time.Second || opts.ollamaURL != "http://127.0.0.1:12345" || opts.embedModel != "test-embed" {
		t.Fatalf("параметры модели не разобраны: %+v", opts)
	}
	if opts.sourceID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("source: %q", opts.sourceID)
	}
}

func TestParseOptionsRejectsBadInput(t *testing.T) {
	t.Setenv("OLLAMA_URL", "http://127.0.0.1:11434")
	t.Setenv("OLLAMA_EMBED_MODEL", "nomic-embed-text")
	t.Run("нет DATABASE_URL", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "")
		if _, err := parseOptions(nil); err == nil {
			t.Fatal("ожидалась ошибка")
		}
	})
	t.Setenv("DATABASE_URL", "postgres://activity@127.0.0.1:5432/activity_descriptor?sslmode=disable")
	for name, args := range map[string][]string{
		"негативный limit": {"-limit", "-1"},
		"нулевой timeout":  {"-timeout", "0s"},
		"плохой UUID":      {"-source", "not-a-uuid"},
		"лишние аргументы": {"лишний"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseOptions(args); err == nil {
				t.Fatalf("ожидалась ошибка для %v", args)
			}
		})
	}
}

func TestBackfillContinuesAfterFailures(t *testing.T) {
	ids := []pgtype.UUID{testUUID(1), testUUID(2), testUUID(3)}
	var indexed []byte
	result := backfill(context.Background(), ids, time.Second, false, func(ctx context.Context, id pgtype.UUID) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("у источника нет таймаута")
		}
		indexed = append(indexed, id.Bytes[15])
		if id.Bytes[15] == 2 {
			return errors.New("модель недоступна")
		}
		return nil
	})
	if result.Indexed != 2 || result.Failed != 1 {
		t.Fatalf("итог: %+v", result)
	}
	if len(indexed) != 3 {
		t.Fatalf("обработаны не все источники: %v", indexed)
	}
}

func TestBackfillAppliesPerSourceTimeout(t *testing.T) {
	ids := []pgtype.UUID{testUUID(1), testUUID(2)}
	result := backfill(context.Background(), ids, 20*time.Millisecond, false, func(ctx context.Context, id pgtype.UUID) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if result.Indexed != 0 || result.Failed != 2 {
		t.Fatalf("таймаут источника не сработал: %+v", result)
	}
}

func TestBackfillStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := 0
	result := backfill(ctx, []pgtype.UUID{testUUID(1)}, time.Second, false, func(context.Context, pgtype.UUID) error {
		called++
		return nil
	})
	if called != 0 || result.Indexed != 0 || result.Failed != 0 {
		t.Fatalf("после отмены индекс не должен вызываться: called=%d %+v", called, result)
	}
}

func TestBackfillEmptyList(t *testing.T) {
	result := backfill(context.Background(), nil, time.Second, false, func(context.Context, pgtype.UUID) error {
		t.Fatal("индексатор не должен вызываться")
		return nil
	})
	if result.Indexed != 0 || result.Failed != 0 {
		t.Fatalf("итог: %+v", result)
	}
}

func TestParseOptionsTrimsOllamaDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://activity@127.0.0.1:5432/activity_descriptor?sslmode=disable")
	t.Setenv("OLLAMA_URL", "  http://127.0.0.1:11434/  ")
	t.Setenv("OLLAMA_EMBED_MODEL", "  nomic-embed-text  ")
	opts, err := parseOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.ollamaURL != "http://127.0.0.1:11434" || opts.embedModel != "nomic-embed-text" {
		t.Fatalf("окружение не нормализовано: %+v", opts)
	}
	if strings.Contains(opts.ollamaURL, "/ ") {
		t.Fatalf("адрес: %q", opts.ollamaURL)
	}
}
