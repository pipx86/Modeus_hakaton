// Команда pilot-audit проверяет готовность данных пилота: источники по
// профессии (карточка 4) и структурированные данные образовательной программы
// (карточка 5). Команда только читает данные: PostgreSQL, JSON-снимок
// (-dataset) или встроенный демонстрационный набор (-fixture).
//
// Код возврата 0 означает, что отчёт построен; статус может быть «готово» или
// «требует внимания». С -strict статус «требует внимания» даёт код 1, чтобы
// поставить аудит в CI. Код 2 означает ошибку конфигурации, снимка или
// недоступную базу.
//
// Примеры:
//
//	go run ./cmd/pilot-audit -profession "Аналитик данных" -fixture
//	go run ./cmd/pilot-audit -fixture -program program-data-analyst
//	go run ./cmd/pilot-audit -dataset testdata/pilot/sources-sample.json -profession "Аналитик данных"
//	go run ./cmd/pilot-audit -dataset testdata/pilot/program-sample.json -program program-data-analyst-2026
//	go run ./cmd/pilot-audit -profession "Аналитик данных" -fixture -strict
//	DATABASE_URL=... go run ./cmd/pilot-audit -profession "Аналитик данных"
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
	strict     bool
	dataset    string
	fixture    bool
	dsn        string
}

// offline сообщает, что данные берутся не из PostgreSQL.
func (o options) offline() bool {
	return o.dataset != "" || o.fixture
}

// auditOutput объединяет отчёты в один JSON-объект, когда запрошены обе цели.
type auditOutput struct {
	Sources *pilot.SourceReport `json:"sources,omitempty"`
	Program *pilot.ProgramAudit `json:"program,omitempty"`
}

// ready сообщает, все ли построенные отчёты имеют статус «готово». Для
// -strict достаточно одного отчёта «требует внимания».
func (o auditOutput) ready() bool {
	if o.Sources != nil && !o.Sources.Ready {
		return false
	}
	if o.Program != nil && !o.Program.Ready {
		return false
	}
	return true
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

	reader, closeReader, err := openReader(ctx, opts)
	if err != nil {
		fmt.Fprintln(stderr, "pilot-audit:", err)
		return 2
	}
	defer closeReader()

	var output auditOutput
	if opts.profession != "" {
		rows, err := reader.SourcesByProfession(ctx, opts.profession)
		if err != nil {
			fmt.Fprintf(stderr, "pilot-audit: прочитать источники: %v\n", err)
			return 2
		}
		matched, err := reader.MatchedSourceIDs(ctx, opts.profession)
		if err != nil {
			fmt.Fprintf(stderr, "pilot-audit: сопоставить источники с анализатором: %v\n", err)
			return 2
		}
		report := pilot.BuildSourceReport(opts.profession, rows, matched)
		output.Sources = &report
	}
	if opts.program != "" {
		facts, err := reader.LoadProgramFacts(ctx, opts.program)
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
	} else {
		if output.Sources != nil {
			fmt.Fprint(stdout, pilot.FormatSourceReport(*output.Sources))
		}
		if output.Sources != nil && output.Program != nil {
			fmt.Fprintln(stdout)
		}
		if output.Program != nil {
			fmt.Fprint(stdout, pilot.FormatProgramReport(*output.Program))
		}
	}

	if opts.strict && !output.ready() {
		fmt.Fprintln(stderr, "pilot-audit: статус «требует внимания», включён -strict")
		return 1
	}
	return 0
}

// openReader выбирает источник данных: снимок, встроенный набор или PostgreSQL.
// В офлайн-режимах пул не создаётся и DATABASE_URL не читается.
func openReader(ctx context.Context, opts options) (pilot.Reader, func(), error) {
	switch {
	case opts.fixture:
		return pilot.Fixture(), func() {}, nil
	case opts.dataset != "":
		dataset, err := pilot.LoadDataset(opts.dataset)
		if err != nil {
			return nil, func() {}, err
		}
		return dataset, func() {}, nil
	default:
		pool, err := pgxpool.New(ctx, opts.dsn)
		if err != nil {
			return nil, func() {}, errors.New("неверный DSN PostgreSQL; проверьте -dsn или DATABASE_URL")
		}
		if err := pool.Ping(ctx); err != nil {
			pool.Close()
			return nil, func() {}, fmt.Errorf("нет подключения к PostgreSQL: %w", err)
		}
		return pilot.New(pool), pool.Close, nil
	}
}

// parseOptions разбирает флаги. DSN по умолчанию берётся из DATABASE_URL и
// требуется только для режима PostgreSQL; в режимах -dataset и -fixture база не
// нужна. Нужна хотя бы одна цель — профессия или программа.
func parseOptions(args []string, stderr io.Writer) (options, error) {
	opts := options{dsn: strings.TrimSpace(os.Getenv("DATABASE_URL"))}
	flags := flag.NewFlagSet("pilot-audit", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.profession, "profession", "", "профессия для аудита источников")
	flags.StringVar(&opts.program, "program", "", "ID образовательной программы")
	flags.BoolVar(&opts.json, "json", false, "вывести отчёт в JSON")
	flags.BoolVar(&opts.strict, "strict", false, "вернуть код 1, если статус «требует внимания»")
	flags.StringVar(&opts.dataset, "dataset", "", "JSON-снимок данных для офлайн-аудита (PostgreSQL не нужен)")
	flags.BoolVar(&opts.fixture, "fixture", false, "встроенный демонстрационный набор (PostgreSQL не нужен)")
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
	opts.dataset = strings.TrimSpace(opts.dataset)
	if opts.dataset != "" && opts.fixture {
		return options{}, errors.New("нельзя одновременно указывать -dataset и -fixture")
	}
	if opts.profession == "" && opts.program == "" {
		return options{}, errors.New("нужна хотя бы одна цель: укажите -profession и/или -program")
	}
	if !opts.offline() && opts.dsn == "" {
		return options{}, errors.New("не задан DSN: укажите -dsn, переменную DATABASE_URL или офлайн-режим -dataset / -fixture")
	}
	// В базе ID программы — UUID. В снимке и фикстуре допускается любая
	// непустая строка: снимок может быть обезличен.
	if opts.program != "" && !opts.offline() {
		var id pgtype.UUID
		if err := id.Scan(opts.program); err != nil || !id.Valid {
			return options{}, fmt.Errorf("некорректный UUID программы: %q", opts.program)
		}
	}
	return opts, nil
}
