// Команда data-audit проверяет сохранённое дерево и записывает отчёт в JSON.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"activity-descriptor/internal/store/db"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type issue struct {
	Code     string      `json:"code"`
	Priority string      `json:"priority"`
	RunID    pgtype.UUID `json:"run_id"`
	NodeID   pgtype.UUID `json:"node_id"`
}
type report struct {
	GeneratedAt   time.Time `json:"generated_at"`
	Issues        []issue   `json:"issues"`
	CriticalCount int       `json:"critical_count"`
}

// Критерии P0 отражают проблемы, из-за которых дереву нельзя доверять.
func priority(code string) string {
	switch code {
	case "EMPTY_TITLE", "ORPHAN_NODE", "DUPLICATE_SIBLING_TITLE", "MISSING_EVIDENCE", "CYCLE_DETECTED":
		return "P0"
	default:
		return "P0"
	}
}
func makeReport(rows []db.ListTreeDataIssuesRow, now time.Time) report {
	r := report{GeneratedAt: now.UTC(), Issues: make([]issue, 0, len(rows))}
	for _, row := range rows {
		p := priority(row.Code)
		r.Issues = append(r.Issues, issue{Code: row.Code, Priority: p, RunID: row.RunID, NodeID: row.NodeID})
		if p == "P0" {
			r.CriticalCount++
		}
	}
	return r
}
func saveReport(dir string, value report) (string, error) {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", fmt.Errorf("создать папку отчётов: %w", err)
	}
	file, err := os.CreateTemp(dir, "db-audit-*.json")
	if err != nil {
		return "", fmt.Errorf("создать отчёт: %w", err)
	}
	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		file.Close()
		return "", fmt.Errorf("записать отчёт: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("закрыть отчёт: %w", err)
	}
	return filepath.Clean(file.Name()), nil
}
func check(ctx context.Context, databaseURL, dir string) (string, int, error) {
	if databaseURL == "" {
		return "", 0, errors.New("переменная DATABASE_URL не задана")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return "", 0, errors.New("неверные настройки PostgreSQL")
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return "", 0, errors.New("нет соединения с PostgreSQL")
	}
	rows, err := db.New(pool).ListTreeDataIssues(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("выполнить проверку данных: %w", err)
	}
	value := makeReport(rows, time.Now())
	path, err := saveReport(dir, value)
	if err != nil {
		return "", 0, err
	}
	return path, value.CriticalCount, nil
}
func main() {
	dir := os.Getenv("DATA_REPORTS_DIR")
	if dir == "" {
		dir = filepath.Join("data", "reports")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path, count, err := check(ctx, os.Getenv("DATABASE_URL"), dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Printf("Отчёт: %s\nКритических проблем: %d\n", path, count)
	if count > 0 {
		os.Exit(1)
	}
}
