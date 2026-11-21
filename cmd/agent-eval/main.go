// Command agent-eval runs the three-agent pipeline over a small golden set with
// a scripted, offline LLM client. It checks the structural guarantees of the
// result (status, tree hierarchy, evidence, verbatim quotes, conflicts,
// transcript size) and prints a comparable metrics table.
//
// With -reference it also compares the tree of one case (the last one by
// default, or -case NAME) with a manual reference via eval.CompareTrees and
// prints precision/recall/F1, missing/extra nodes, level mismatches and
// ambiguous nodes. -enforce-thresholds turns the reference CI gates into
// non-zero exit codes. Without these flags the command behaves exactly as
// before.
//
// The command is deterministic and needs no network, database or API keys. It
// exits non-zero when a case fails or a fixture cannot be loaded.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"activity-descriptor/internal/agent/eval"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// options — разобранные флаги команды. reportFormat относится только к отчёту
// по эталону: основной отчёт кейсов по-прежнему пишется флагом -out.
type options struct {
	casesPath         string
	outPath           string
	referencePath     string
	reportFormat      string
	caseName          string
	enforceThresholds bool
}

func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseOptions(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	cases, source, err := loadCases(opts.casesPath)
	if err != nil {
		fmt.Fprintf(stderr, "agent-eval: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "AGENT PIPELINE EVAL (offline scripted LLM)\ncases: %d (%s)\n\n", len(cases), source)

	report := eval.RunAll(context.Background(), cases)
	if err := eval.WriteTable(stdout, report); err != nil {
		fmt.Fprintf(stderr, "agent-eval: %v\n", err)
		return 1
	}
	if opts.outPath != "" {
		var buffer bytes.Buffer
		if err := eval.WriteJSON(&buffer, report); err != nil {
			fmt.Fprintf(stderr, "agent-eval: %v\n", err)
			return 1
		}
		if err := os.WriteFile(opts.outPath, buffer.Bytes(), 0o644); err != nil {
			fmt.Fprintf(stderr, "agent-eval: write %s: %v\n", opts.outPath, err)
			return 1
		}
		fmt.Fprintf(stdout, "report: %s\n", opts.outPath)
	}

	if opts.referencePath != "" {
		if code := reportReference(opts, report, stdout, stderr); code != 0 {
			return code
		}
	}

	if report.Failed > 0 {
		fmt.Fprintf(stderr, "agent-eval: %d of %d cases failed\n", report.Failed, report.Passed+report.Failed)
		return 1
	}
	return 0
}

// reportReference загружает ручной эталон и сравнивает его с деревом выбранного
// кейса. Код 2 — ошибка конфигурации (эталон или имя кейса), код 1 — эталон
// загрузился, но сравнение или пороги не прошли.
func reportReference(opts options, report eval.Report, stdout, stderr io.Writer) int {
	ref, err := eval.LoadReference(opts.referencePath)
	if err != nil {
		fmt.Fprintf(stderr, "agent-eval: %v\n", err)
		return 2
	}

	index := len(report.Cases) - 1
	if opts.caseName != "" {
		index = -1
		for i := range report.Cases {
			if report.Cases[i].Name == opts.caseName {
				index = i
				break
			}
		}
		if index < 0 {
			fmt.Fprintf(stderr, "agent-eval: кейс %q не найден среди запущенных\n", opts.caseName)
			return 2
		}
	}
	if index < 0 {
		fmt.Fprintln(stderr, "agent-eval: нет кейсов для сравнения с эталоном")
		return 2
	}

	selected := report.Cases[index]
	if len(selected.Tree) == 0 {
		fmt.Fprintf(stderr, "agent-eval: у кейса %q нет сгенерированного дерева для сравнения\n", selected.Name)
		return 1
	}
	referenceReport := eval.CompareTrees(ref, selected.Tree)
	if referenceReport.Error != "" {
		fmt.Fprintf(stderr, "agent-eval: сравнение с эталоном: %s\n", referenceReport.Error)
		return 1
	}

	switch opts.reportFormat {
	case "json":
		data, err := json.MarshalIndent(referenceReport, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "agent-eval: сериализовать отчёт по эталону: %v\n", err)
			return 1
		}
		_, _ = stdout.Write(append(data, '\n'))
	default:
		fmt.Fprint(stdout, eval.FormatReferenceReport(selected.Name, ref, referenceReport))
	}

	if opts.enforceThresholds {
		if violations := eval.CheckReferenceThresholds(referenceReport); len(violations) > 0 {
			fmt.Fprintf(stderr, "agent-eval: пороги эталона не выполнены (%d):\n", len(violations))
			for _, violation := range violations {
				fmt.Fprintf(stderr, "  - %s\n", violation)
			}
			return 1
		}
	}
	return 0
}

// parseOptions разбирает флаги. Флаги, относящиеся к эталону (-report, -case,
// -enforce-thresholds), без -reference считаются ошибкой конфигурации, чтобы
// опечатка не оставила проверку эталона молча выключенной.
func parseOptions(args []string, stderr io.Writer) (options, error) {
	opts := options{reportFormat: "text"}
	flags := flag.NewFlagSet("agent-eval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.casesPath, "cases", "", "case JSON file or directory (default: embedded golden set)")
	flags.StringVar(&opts.outPath, "out", "", "optional path for the JSON report")
	flags.StringVar(&opts.referencePath, "reference", "", "manual reference JSON to compare with the selected case")
	flags.StringVar(&opts.reportFormat, "report", opts.reportFormat, "reference report format: text or json")
	flags.StringVar(&opts.caseName, "case", "", "case whose tree is compared with -reference (default: last case)")
	flags.BoolVar(&opts.enforceThresholds, "enforce-thresholds", false, "exit 1 when reference thresholds fail")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}

	reportSet, caseSet := false, false
	flags.Visit(func(parsed *flag.Flag) {
		switch parsed.Name {
		case "report":
			reportSet = true
		case "case":
			caseSet = true
		}
	})

	opts.casesPath = strings.TrimSpace(opts.casesPath)
	opts.outPath = strings.TrimSpace(opts.outPath)
	opts.referencePath = strings.TrimSpace(opts.referencePath)
	opts.reportFormat = strings.ToLower(strings.TrimSpace(opts.reportFormat))
	opts.caseName = strings.TrimSpace(opts.caseName)

	if opts.reportFormat != "text" && opts.reportFormat != "json" {
		return usageError(stderr, "неизвестный формат -report %q: допустимо text или json", opts.reportFormat)
	}
	if opts.referencePath == "" {
		switch {
		case reportSet:
			return usageError(stderr, "флаг -report требует -reference")
		case caseSet:
			return usageError(stderr, "флаг -case требует -reference")
		case opts.enforceThresholds:
			return usageError(stderr, "флаг -enforce-thresholds требует -reference")
		}
	}
	return opts, nil
}

// usageError печатает ошибку конфигурации и возвращает её вызывающему. Ошибки
// самого flag.Parse уже напечатаны пакетом flag, поэтому run их не дублирует.
func usageError(stderr io.Writer, format string, args ...any) (options, error) {
	err := fmt.Errorf(format, args...)
	fmt.Fprintf(stderr, "agent-eval: %v\n", err)
	return options{}, err
}

// loadCases resolves the -cases flag: empty means the embedded golden set, a
// directory loads every "*.json" case in it, and a file loads exactly one case.
func loadCases(path string) ([]eval.Case, string, error) {
	if path == "" {
		cases, err := eval.DefaultCases()
		if err != nil {
			return nil, "", fmt.Errorf("embedded cases: %w", err)
		}
		return cases, "internal/agent/eval/testdata/cases", nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", fmt.Errorf("cases path: %w", err)
	}
	if info.IsDir() {
		cases, err := eval.LoadCasesDir(path)
		if err != nil {
			return nil, "", err
		}
		return cases, path, nil
	}
	tc, err := eval.LoadCaseFile(path)
	if err != nil {
		return nil, "", err
	}
	return []eval.Case{tc}, path, nil
}
