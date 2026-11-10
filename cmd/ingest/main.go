package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"activity-descriptor/internal/ingestion"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) >= 2 && os.Args[1] == "taxonomy" {
		return runTaxonomy(os.Args[2:])
	}
	if len(os.Args) >= 2 && os.Args[1] == "education" {
		return runEducation(os.Args[2:])
	}
	if len(os.Args) >= 2 && os.Args[1] == "standard" {
		return runStandard(os.Args[2:])
	}
	if len(os.Args) < 2 || os.Args[1] != "vacancies" {
		return fmt.Errorf("использование: ingest vacancies -file путь -format json|csv либо -query запрос")
	}
	flags := flag.NewFlagSet("vacancies", flag.ContinueOnError)
	file := flags.String("file", "", "файл вакансий")
	format := flags.String("format", "json", "формат файла: json или csv")
	query := flags.String("query", "", "поисковый запрос hh.ru")
	pages := flags.Int("pages", 1, "число страниц hh.ru, не более 20")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if (*file == "") == (*query == "") {
		return fmt.Errorf("укажите ровно один вариант: -file или -query")
	}
	ctx := context.Background()
	var items []ingestion.Vacancy
	var err error
	if *file != "" {
		items, err = ingestion.ReadVacancyFile(*file, *format)
	} else {
		items, err = ingestion.FetchHH(ctx, ingestion.DefaultHTTPClient(), "https://api.hh.ru/vacancies", *query, os.Getenv("HH_USER_AGENT"), os.Getenv("HH_ACCESS_TOKEN"), *pages)
	}
	if err != nil {
		return err
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("задайте DATABASE_URL")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	counts, err := ingestion.SaveVacancies(ctx, pool, items)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(counts)
}

func runStandard(args []string) error {
	flags := flag.NewFlagSet("standard", flag.ContinueOnError)
	file := flags.String("file", "", "файл документа")
	format := flags.String("format", "json", "pdf или json")
	title := flags.String("title", "", "название для PDF")
	key := flags.String("key", "", "постоянный ключ документа")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *file == "" || *key == "" {
		return fmt.Errorf("укажите -file и постоянный -key")
	}
	data, err := ingestion.ReadStandard(*file, *format, *title)
	if err != nil {
		return err
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("задайте DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	counts, err := ingestion.SaveStandard(ctx, pool, *key, data)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(counts)
}

func runEducation(args []string) error {
	flags := flag.NewFlagSet("education", flag.ContinueOnError)
	file := flags.String("file", "", "файл программы")
	format := flags.String("format", "json", "json, text или pdf")
	title := flags.String("title", "", "название программы для text или pdf")
	key := flags.String("key", "", "постоянный ключ программы")
	taxonomy := flags.String("taxonomy", "", "UUID таксономии для сопоставления")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *file == "" || *key == "" {
		return fmt.Errorf("укажите -file и постоянный -key")
	}
	var data ingestion.EducationProgram
	var err error
	switch *format {
	case "json":
		var f *os.File
		f, err = os.Open(*file)
		if err != nil {
			return err
		}
		defer f.Close()
		data, err = ingestion.ReadEducation(f)
	case "text":
		info, e := os.Stat(*file)
		if e != nil {
			return e
		}
		if info.Size() > 20<<20 {
			return fmt.Errorf("файл больше 20 МБ")
		}
		var b []byte
		b, err = os.ReadFile(*file)
		if err != nil {
			return err
		}
		if len(b) > 20<<20 {
			return fmt.Errorf("файл больше 20 МБ")
		}
		data, err = ingestion.ExtractEducationText(string(b), *title)
	case "pdf":
		var doc ingestion.Standard
		doc, err = ingestion.ReadStandard(*file, "pdf", *title)
		if err != nil {
			return err
		}
		data, err = ingestion.ExtractEducationText(doc.Content, *title)
	default:
		return fmt.Errorf("формат должен быть json, text или pdf")
	}
	if err != nil {
		return err
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("задайте DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	report, err := ingestion.SaveEducation(ctx, pool, *key, *taxonomy, data)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}

func runTaxonomy(args []string) error {
	flags := flag.NewFlagSet("taxonomy", flag.ContinueOnError)
	file := flags.String("file", "", "файл справочника")
	format := flags.String("format", "json", "json или csv")
	name := flags.String("name", "", "название справочника для CSV")
	key := flags.String("key", "", "постоянный ключ источника")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *file == "" || *key == "" {
		return fmt.Errorf("укажите -file и постоянный -key")
	}
	f, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := ingestion.ReadTaxonomy(f, *format, *name)
	if err != nil {
		return err
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("задайте DATABASE_URL")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	id, err := ingestion.SaveTaxonomy(context.Background(), pool, *key, data)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"taxonomy_id": id, "items": len(data.Items)})
}
