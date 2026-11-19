package api_test

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v4"
)

func TestPostgresCostHistoryFallsBackToRunDurations(t *testing.T) {
	pool, pg := testPostgresAPI(t)
	ctx := context.Background()
	typ := domain.RunTypeBatch
	missingTable := &pgconn.PgError{Code: "42P01"}
	duration := func(seconds float64) *pgxmock.Rows {
		return pgxmock.NewRows([]string{"id", "duration"}).AddRow(dbRun, seconds)
	}
	pool.ExpectQuery("WITH sample AS").WithArgs("Инженер", string(typ), pgxmock.AnyArg()).WillReturnError(missingTable)
	pool.ExpectQuery("EXTRACT.*FROM runs").WithArgs("Инженер", string(typ), pgxmock.AnyArg()).WillReturnRows(duration(40))
	pool.ExpectQuery("EXTRACT.*FROM runs").WithArgs(string(typ), pgxmock.AnyArg()).WillReturnRows(duration(50))
	pool.ExpectQuery("FROM llm_usage").WillReturnError(missingTable)
	history, err := pg.CostHistory(ctx, " Инженер ", typ)
	if err != nil || len(history.ProfessionRuns) != 1 || history.ProfessionRuns[0].DurationSeconds != 40 ||
		len(history.GlobalRuns) != 1 || history.GlobalRuns[0].DurationSeconds != 50 ||
		history.RequestStats.RequestCount != 0 {
		t.Fatalf("история без таблиц LLM: %#v, %v", history, err)
	}
	pool.ExpectQuery("WITH sample AS").WithArgs("Инженер", string(typ), pgxmock.AnyArg()).WillReturnError(errTestDB)
	if _, err := pg.CostHistory(ctx, "Инженер", typ); !errors.Is(err, errTestDB) {
		t.Fatalf("обычный сбой истории потерян: %v", err)
	}
	pool.ExpectQuery("WITH sample AS").WithArgs("Инженер", string(typ), pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "duration", "attribution", "requests", "usage_rows", "prompt", "completion", "total", "cost"}).
			AddRow(dbRun, 32.0, "run_id", int64(2), int64(1), int64(15), int64(2), int64(17), pgtype.Numeric{}))
	pool.ExpectQuery("EXTRACT.*FROM runs").WithArgs(string(typ), pgxmock.AnyArg()).WillReturnRows(duration(50))
	pool.ExpectQuery("FROM llm_usage").
		WillReturnRows(pgxmock.NewRows([]string{"requests", "tokens", "latencies", "latency_sum", "cost_count", "cost_tokens", "cost"}).
			AddRow(int64(2), int64(17), int64(1), int64(40), int64(1), int64(17), 0.05))
	history, err = pg.CostHistory(ctx, "Инженер", typ)
	if err != nil || len(history.ProfessionRuns) != 1 || history.ProfessionRuns[0].RequestCount != 2 ||
		history.ProfessionRuns[0].TotalTokens != 17 || history.ProfessionRuns[0].CostUSD != nil ||
		history.RequestStats.TotalTokens != 17 || history.RequestStats.CostSumUSD != 0.05 {
		t.Fatalf("агрегированная история запросов: %#v, %v", history, err)
	}
}

func TestPostgresCostPrefersExactAttributionAndMarksOverlaps(t *testing.T) {
	pool, pg := testPostgresAPI(t)
	ctx := context.Background()
	from := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	until := from.Add(time.Minute)
	run := domain.Run{ID: dbRun, StartedAt: &from, FinishedAt: &until}
	facts := func() *pgxmock.Rows {
		return pgxmock.NewRows([]string{"agent", "model", "status", "prompt", "completion", "total", "latency", "cost"})
	}
	pool.ExpectQuery("WHERE req.run_id =").WithArgs(dbRun).
		WillReturnRows(facts().AddRow("analyst", "test-model", "ok", 5, 2, 7, 9,
			pgtype.Numeric{Int: big.NewInt(25), Exp: -2, Valid: true}))
	result, err := pg.RunLLMFacts(ctx, &run)
	if err != nil || result.Attribution != "run_id" || result.Ambiguous || len(result.Requests) != 1 ||
		result.Requests[0].CostUSD == nil || *result.Requests[0].CostUSD != 0.25 {
		t.Fatalf("точная привязка запроса: %#v, %v", result, err)
	}
	pool.ExpectQuery("WHERE req.run_id =").WithArgs(dbRun).WillReturnRows(facts())
	pool.ExpectQuery("FROM runs other").WithArgs(dbRun, until, from).
		WillReturnRows(pgxmock.NewRows([]string{"overlaps"}).AddRow(true))
	pool.ExpectQuery("WHERE req.run_id IS NULL").WithArgs(from, until).
		WillReturnRows(facts().AddRow("analyst", "test-model", "ok", 5, 2, 7, 9, pgtype.Numeric{}))
	result, err = pg.RunLLMFacts(ctx, &run)
	if err != nil || result.Attribution != "time_window" || !result.Ambiguous || len(result.Requests) != 1 ||
		result.Requests[0].TotalTokens != 7 || result.Requests[0].CostUSD != nil {
		t.Fatalf("привязка по пересекающемуся окну: %#v, %v", result, err)
	}
	pool.ExpectQuery("WHERE req.run_id =").WithArgs(dbRun).WillReturnError(errTestDB)
	if _, err := pg.RunLLMFacts(ctx, &run); !errors.Is(err, errTestDB) {
		t.Fatalf("сбой SQL-запроса: %v", err)
	}
}
