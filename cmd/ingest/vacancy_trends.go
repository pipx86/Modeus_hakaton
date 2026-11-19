package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"activity-descriptor/internal/ingestion"

	"github.com/jackc/pgx/v5/pgxpool"
)

func runVacancyTrends(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("vacancy-trends", flag.ContinueOnError)
	query := flags.String("query", "", "часть названия вакансии")
	fromText := flags.String("from", "", "начало периода (ГГГГ-ММ-ДД, включительно)")
	untilText := flags.String("until", "", "конец периода (ГГГГ-ММ-ДД, исключая)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	from, err := time.Parse(time.DateOnly, *fromText)
	if err != nil {
		return fmt.Errorf("-from должен быть датой ГГГГ-ММ-ДД")
	}
	until, err := time.Parse(time.DateOnly, *untilText)
	if err != nil {
		return fmt.Errorf("-until должен быть датой ГГГГ-ММ-ДД")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("задайте DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("неверная конфигурация PostgreSQL")
	}
	defer pool.Close()
	report, err := ingestion.VacancyPublicationTrend(ctx, pool, *query, from, until)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(report)
}
