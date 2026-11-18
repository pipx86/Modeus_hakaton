// Command agent-eval runs the three-agent pipeline over a small golden set with
// a scripted, offline LLM client. It checks the structural guarantees of the
// result (status, tree hierarchy, evidence, verbatim quotes, conflicts,
// transcript size) and prints a comparable metrics table.
//
// The command is deterministic and needs no network, database or API keys. It
// exits non-zero when a case fails or a fixture cannot be loaded.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"activity-descriptor/internal/agent/eval"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent-eval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	casesPath := flags.String("cases", "", "case JSON file or directory (default: embedded golden set)")
	outPath := flags.String("out", "", "optional path for the JSON report")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	cases, source, err := loadCases(*casesPath)
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
	if *outPath != "" {
		var buffer bytes.Buffer
		if err := eval.WriteJSON(&buffer, report); err != nil {
			fmt.Fprintf(stderr, "agent-eval: %v\n", err)
			return 1
		}
		if err := os.WriteFile(*outPath, buffer.Bytes(), 0o644); err != nil {
			fmt.Fprintf(stderr, "agent-eval: write %s: %v\n", *outPath, err)
			return 1
		}
		fmt.Fprintf(stdout, "report: %s\n", *outPath)
	}
	if report.Failed > 0 {
		fmt.Fprintf(stderr, "agent-eval: %d of %d cases failed\n", report.Failed, report.Passed+report.Failed)
		return 1
	}
	return 0
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
