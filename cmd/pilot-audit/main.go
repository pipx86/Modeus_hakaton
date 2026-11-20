// Команда pilot-audit проверяет готовность данных пилота: источники по
// профессии (карточка 4) и структурированные данные образовательной программы
// (карточка 5). Команда только читает базу. Если отчёт построен, код возврата 0
// независимо от статуса «готово / требует внимания»; код 2 означает ошибку
// конфигурации или недоступную базу.
//
// Примеры:
//
//	go run ./cmd/pilot-audit -profession "аналитик данных"
//	go run ./cmd/pilot-audit -program <uuid программы>
//	go run ./cmd/pilot-audit -profession "аналитик данных" -program <uuid> -json
//	DATABASE_URL=... go run ./cmd/pilot-audit -profession "аналитик данных"
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"activity-descriptor/internal/pilot"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const auditTimeout = 30 * time.Second

type options struct {
	profession string
	program    string
	json       bool
	dsn        string
}

// auditOutput объединяет отчёты в один JSON-объект, когда запрошены обе цели.
type auditOutput struct {
	Sources *pilot.SourceReport `json:"sources,omitempty"`
	Program *pilot.ProgramAudit `json:"program,omitempty"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run отделён от main, чтобы код возврата было удобно проверять без os.Exit.
func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseOptions(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, "pilot-audit:", err)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), auditTimeout)
	defer cancel()
	pool, err := pgxpool.New(ctx, opts.dsn)
	if err != nil {
		fmt.Fprintln(stderr, "pilot-audit: неверный DSN PostgreSQL; проверьте -dsn или DATABASE_URL")
		return 2
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		fmt.Fprintf(stderr, "pilot-audit: нет подключения к PostgreSQL: %v\n", err)
		return 2
	}

	store := pilot.New(pool)
	var output auditOutput
	if opts.profession != "" {
		rows, err := store.SourcesByProfession(ctx, opts.profession)
		if err != nil {
			fmt.Fprintf(stderr, "pilot-audit: прочитать источники: %v\n", err)
			return 2
		}
		matched, err := store.MatchedSourceIDs(ctx, opts.profession)
		if err != nil {
			fmt.Fprintf(stderr, "pilot-audit: сопоставить источники с анализатором: %v\n", err)
			return 2
		}
		report := pilot.BuildSourceReport(opts.profession, rows, matched)
		output.Sources = &report
	}
	if opts.program != "" {
		facts, err := store.LoadProgramFacts(ctx, opts.program)
		if err != nil {
			fmt.Fprintf(stderr, "pilot-audit: прочитать программу: %v\n", err)
			return 2
		}
		report := pilot.BuildProgramReport(facts)
		output.Program = &report
	}

	if opts.json {
		data, err := pilot.ToJSON(output)
		if err != nil {
			fmt.Fprintf(stderr, "pilot-audit: %v\n", err)
			return 2
		}
		_, _ = stdout.Write(data)
		return 0
	}

	if output.Sources != nil {
		fmt.Fprint(stdout, pilot.FormatSourceReport(*output.Sources))
	}
	if output.Sources != nil && output.Program != nil {
		fmt.Fprintln(stdout)
	}
	if output.Program != nil {
		fmt.Fprint(stdout, pilot.FormatProgramReport(*output.Program))
	}
	return 0
}

// parseOptions разбирает флаги. DSN по умолчанию берётся из DATABASE_URL;
// нужна хотя бы одна цель — профессия или программа.
func parseOptions(args []string, stderr io.Writer) (options, error) {
	opts := options{dsn: strings.TrimSpace(os.Getenv("DATABASE_URL"))}
	flags := flag.NewFlagSet("pilot-audit", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.profession, "profession", "", "профессия для аудита источников")
	flags.StringVar(&opts.program, "program", "", "ID образовательной программы (UUID)")
	flags.BoolVar(&opts.json, "json", false, "вывести отчёт в JSON")
	flags.StringVar(&opts.dsn, "dsn", opts.dsn, "строка подключения PostgreSQL (по умолчанию DATABASE_URL)")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() > 0 {
		return options{}, fmt.Errorf("неизвестные аргументы: %s", strings.Join(flags.Args(), " "))
	}

	opts.profession = strings.TrimSpace(opts.profession)
	opts.program = strings.TrimSpace(opts.program)
	opts.dsn = strings.TrimSpace(opts.dsn)
	if opts.profession == "" && opts.program == "" {
		return options{}, errors.New("нужна хотя бы одна цель: укажите -profession и/или -program")
	}
	if opts.dsn == "" {
		return options{}, errors.New("не задан DSN: укажите -dsn или переменную DATABASE_URL")
	}
	if opts.program != "" {
		var id pgtype.UUID
		if err := id.Scan(opts.program); err != nil || !id.Valid {
			return options{}, fmt.Errorf("некорректный UUID программы: %q", opts.program)
		}
	}
	return opts, nil
}
