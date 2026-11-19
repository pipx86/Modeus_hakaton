package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"activity-descriptor/internal/ingestion"
	"activity-descriptor/internal/store/db"

	"github.com/jackc/pgx/v5/pgxpool"
)

func runVacancyArchive(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("export-vacancies", flag.ContinueOnError)
	basic := flags.String("basic", "", "JSON с основными полями вакансий")
	detailed := flags.String("detailed", "", "JSON с подробностями вакансий")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || (*basic == "" && *detailed == "") {
		return fmt.Errorf("укажите -basic, -detailed или оба файла")
	}
	vacancies, err := ingestion.ReadVacancyArchive(*basic, *detailed)
	if err != nil {
		return err
	}
	return withImportDatabase(func(ctx context.Context, pool *pgxpool.Pool) error {
		counts, err := ingestion.SaveVacancies(ctx, pool, vacancies)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(counts)
	})
}

func runSkillHistory(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("skill-history", flag.ContinueOnError)
	dir := flags.String("dir", "", "каталог со снимками freq_*.json")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dir == "" || flags.NArg() != 0 {
		return fmt.Errorf("укажите -dir с каталогом снимков")
	}
	entries, err := os.ReadDir(*dir)
	if err != nil {
		return err
	}
	var files []string
	for _, entry := range entries {
		match, _ := filepath.Match("freq_*.json", entry.Name())
		if match && entry.Type().IsRegular() {
			files = append(files, filepath.Join(*dir, entry.Name()))
		}
	}
	if len(files) == 0 {
		return fmt.Errorf("в каталоге нет файлов freq_*.json")
	}
	return withImportDatabase(func(ctx context.Context, pool *pgxpool.Pool) error {
		changed, err := ingestion.ImportSkillSnapshots(ctx, pool, files)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(struct {
			Files   int   `json:"files"`
			Changed int64 `json:"changed"`
		}{Files: len(files), Changed: changed})
	})
}

func withImportDatabase(run func(context.Context, *pgxpool.Pool) error) error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("задайте DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("неверная конфигурация PostgreSQL")
	}
	defer pool.Close()
	return run(ctx, pool)
}

func runForecastAudit(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("forecast-audit", flag.ContinueOnError)
	seriesName := flags.String("series", ingestion.MarketSkillSeries, "серия снимков")
	top := flags.Int("top", 20, "максимальное число навыков в отчёте")
	if err := flags.Parse(args); err != nil {
		return err
	}
	series := strings.TrimSpace(*seriesName)
	if series == "" || len(series) > 267 || *top < 1 || *top > 500 || flags.NArg() != 0 {
		return fmt.Errorf("нужна серия и -top от 1 до 500")
	}
	return withImportDatabase(func(ctx context.Context, pool *pgxpool.Pool) error {
		queries := db.New(pool)
		status, err := queries.GetSkillForecastStatus(ctx, series)
		if err != nil {
			return err
		}
		results, err := queries.GetSkillTrendForecasts(ctx, db.GetSkillTrendForecastsParams{
			Series: series, Months: 1, Direction: "all", SortBy: "audit", ResultLimit: int32(*top),
		})
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{
			"series": series, "snapshots": status.Snapshots, "first_month": status.FirstMonth,
			"last_month": status.LastMonth, "missing_months": status.MissingMonths, "evaluated": results.Total,
			"low_error": results.LowError, "medium_error": results.MediumError, "high_error": results.HighError,
			"forecasts": json.RawMessage(results.Forecasts),
		})
	})
}
