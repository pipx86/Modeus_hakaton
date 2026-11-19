package llmobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"

	"activity-descriptor/internal/llmobs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v4"
)

var errSQL = errors.New("тестовая ошибка SQL")

func telemetryStore(t *testing.T) (pgxmock.PgxPoolIface, *llmobs.Store) {
	t.Helper()
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer pool.Close()
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})
	return pool, llmobs.NewStore(pool)
}

func TestTelemetryRecordsRequestResponseAndUsage(t *testing.T) {
	ctx := context.Background()
	pool, store := telemetryStore(t)
	stamp := time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC)
	payload := json.RawMessage(`{"temperature":0.1}`)
	req := llmobs.Request{
		TraceID: "trace", RunID: "run", Agent: "analyst", Provider: "test", Model: "fixture",
		Params: payload, Attempt: 1, MaxAttempts: 2, StartedAt: &stamp,
	}
	pool.ExpectQuery("INSERT INTO llm_requests").WithArgs("trace", "run", "analyst", "test", "fixture", "", "", nil, payload,
		"running", 1, 2, &stamp).WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("request"))
	id, err := store.CreateRequest(ctx, req)
	if err != nil || id != "request" {
		t.Fatalf("регистрация запроса: %q, %v", id, err)
	}
	pool.ExpectExec("UPDATE llm_requests").WithArgs(id, "success", "", 25, stamp).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.FinishRequest(ctx, id, "success", "", 25, stamp); err != nil {
		t.Fatal(err)
	}
	pool.ExpectExec("UPDATE llm_requests").WithArgs(id, "running", "", 0, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	if err := store.FinishRequest(ctx, id, "", "", 0, time.Time{}); !errors.Is(err, llmobs.ErrNotFound) {
		t.Fatalf("отсутствующий запрос: %v", err)
	}
	pool.ExpectExec("INSERT INTO llm_responses").WithArgs(id, "результат", nil, "valid", 0, "stop", nil).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.SaveResponse(ctx, llmobs.Response{RequestID: id, RawText: "результат", ValidationStatus: "valid", FinishReason: "stop"}); err != nil {
		t.Fatal(err)
	}
	cost := 0.25
	pool.ExpectExec("INSERT INTO llm_usage").WithArgs(id, 16, 4, 20, &cost, "", 25).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.SaveUsage(ctx, llmobs.Usage{RequestID: id, PromptTokens: 16, CompletionTokens: 4, TotalTokens: 20, CostUSD: &cost, LatencyMS: 25}); err != nil {
		t.Fatal(err)
	}
	pool.ExpectExec("INSERT INTO llm_responses").WithArgs(id, "", nil, "", 0, "", nil).WillReturnError(errSQL)
	if err := store.SaveResponse(ctx, llmobs.Response{RequestID: id}); !errors.Is(err, errSQL) {
		t.Fatalf("ошибка записи ответа потеряна: %v", err)
	}
}

func TestTelemetryReadsNullableTimestampsUsageAndCost(t *testing.T) {
	ctx := context.Background()
	pool, store := telemetryStore(t)
	const id = "805cd798-35d0-4637-ab57-00d9cd706624"
	stamp := time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC)
	asText := func(value string) pgtype.Text { return pgtype.Text{String: value, Valid: value != ""} }
	asCount := func(value int32) pgtype.Int4 { return pgtype.Int4{Int32: value, Valid: true} }
	asTime := pgtype.Timestamptz{Time: stamp, Valid: true}
	var uuid pgtype.UUID
	if err := uuid.Scan(id); err != nil {
		t.Fatal(err)
	}
	baseColumns := []string{"id", "trace", "run", "agent", "provider", "model", "version", "hash", "status", "error",
		"started", "finished", "latency", "attempt", "max_attempts", "created"}
	baseRow := []any{id, "trace", asText("run"), "analyst", "test", "fixture", asText("v1"), asText(""),
		"success", asText(""), asTime, pgtype.Timestamptz{}, asCount(5), asCount(1), asCount(2), stamp}
	extended := append(append([]string{}, baseColumns...), "response_id", "raw", "json", "validation",
		"repair", "finish", "received", "usage_id", "prompt", "completion", "total", "cost", "effort", "usage_latency")
	values := append(append([]any{}, baseRow...), uuid, asText("ответ"), []byte(`{"ok":true}`), asText("valid"), asCount(1),
		asText("stop"), asTime, uuid, asCount(15), asCount(4), asCount(19),
		pgtype.Numeric{Int: big.NewInt(250), Exp: -3, Valid: true}, asText("low"), asCount(5))
	pool.ExpectQuery("LEFT JOIN llm_responses").WithArgs(id).WillReturnRows(pgxmock.NewRows(extended).AddRow(values...))
	request, err := store.GetRequest(ctx, id)
	if err != nil || request.RunID != "run" || request.StartedAt == nil || !request.StartedAt.Equal(stamp) ||
		request.FinishedAt != nil || request.Response == nil || request.Response.RawText != "ответ" ||
		request.Response.RepairAttempt != 1 || request.Usage == nil || request.Usage.TotalTokens != 19 ||
		request.Usage.CostUSD == nil || *request.Usage.CostUSD != 0.25 {
		t.Fatalf("запрос с ответом и расходом: %#v, %v", request, err)
	}
	pool.ExpectQuery("LEFT JOIN llm_responses").WithArgs(id).WillReturnError(pgx.ErrNoRows)
	if _, err := store.GetRequest(ctx, id); !errors.Is(err, llmobs.ErrNotFound) {
		t.Fatalf("отсутствующий запрос: %v", err)
	}
	pool.ExpectQuery("ORDER BY created_at DESC").WithArgs("run", "analyst", "success", 100).
		WillReturnRows(pgxmock.NewRows(baseColumns).AddRow(baseRow...))
	list, err := store.ListRequests(ctx, llmobs.ListFilter{RunID: "run", Agent: "analyst", Status: "success", Limit: 501})
	if err != nil || len(list) != 1 || list[0].ID != id || list[0].Attempt != 1 || list[0].FinishedAt != nil {
		t.Fatalf("отфильтрованный список: %#v, %v", list, err)
	}
	pool.ExpectQuery("GROUP BY r.model").WithArgs(stamp).
		WillReturnRows(pgxmock.NewRows([]string{"model", "requests", "prompt", "completion", "total"}).
			AddRow("fixture", int64(2), int64(15), int64(4), int64(19)))
	usage, err := store.UsageByModel(ctx, stamp)
	if err != nil || len(usage) != 1 || usage[0].Requests != 2 || usage[0].TotalTokens != 19 {
		t.Fatalf("расход по моделям: %#v, %v", usage, err)
	}
}

func TestPromptVersionActivationIsAtomic(t *testing.T) {
	pool, store := telemetryStore(t)
	ctx := context.Background()
	for _, fail := range []bool{false, true} {
		pool.ExpectBegin()
		pool.ExpectQuery("SELECT agent FROM prompt_versions").WithArgs("v2").
			WillReturnRows(pgxmock.NewRows([]string{"agent"}).AddRow("analyst"))
		pool.ExpectExec("SET status='archived'").WithArgs("analyst", "v2").
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		pool.ExpectExec("SET status='active'").WithArgs("v2").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		audit := pool.ExpectExec("INSERT INTO prompt_activations").WithArgs("analyst", "v2", "оператор", "проверка")
		if fail {
			audit.WillReturnError(errSQL)
			pool.ExpectRollback()
		} else {
			audit.WillReturnResult(pgxmock.NewResult("INSERT", 1))
			pool.ExpectCommit()
		}
		err := store.ActivatePromptVersion(ctx, "v2", "оператор", "проверка")
		if (fail && !errors.Is(err, errSQL)) || (!fail && err != nil) {
			t.Fatalf("активация при сбое аудита %v: %v", fail, err)
		}
	}
	pool.ExpectBegin()
	pool.ExpectQuery("SELECT agent FROM prompt_versions").WithArgs("missing").WillReturnError(pgx.ErrNoRows)
	pool.ExpectRollback()
	if err := store.ActivatePromptVersion(ctx, "missing", "", ""); !errors.Is(err, llmobs.ErrNotFound) {
		t.Fatalf("версия отсутствует: %v", err)
	}
	stamp := time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC)
	columns := []string{"id", "agent", "version", "hash", "text", "examples", "status", "created", "activated"}
	rows := func() *pgxmock.Rows {
		return pgxmock.NewRows(columns).AddRow("v2", "analyst", "2", "hash", "текст", "", "active", stamp,
			pgtype.Timestamptz{Time: stamp, Valid: true})
	}
	pool.ExpectQuery("ORDER BY activated_at DESC").WithArgs("analyst").WillReturnRows(rows())
	version, err := store.ActivePromptVersion(ctx, "analyst")
	if err != nil || version.ID != "v2" || version.ActivatedAt == nil || !version.ActivatedAt.Equal(stamp) {
		t.Fatalf("активная версия: %#v, %v", version, err)
	}
	pool.ExpectQuery("ORDER BY agent, created_at DESC").WithArgs("analyst").WillReturnRows(rows())
	list, err := store.ListPromptVersions(ctx, "analyst")
	if err != nil || len(list) != 1 || list[0].Status != "active" {
		t.Fatalf("список версий: %#v, %v", list, err)
	}
}
