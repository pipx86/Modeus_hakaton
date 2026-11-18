package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"activity-descriptor/internal/agent/eval"
)

func embeddedCases(t *testing.T) []eval.Case {
	t.Helper()
	cases, err := eval.DefaultCases()
	if err != nil {
		t.Fatalf("DefaultCases: %v", err)
	}
	return cases
}

func writeCase(t *testing.T, dir, name string, tc eval.Case) string {
	t.Helper()
	data, err := json.MarshalIndent(tc, "", "  ")
	if err != nil {
		t.Fatalf("marshal case: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write case: %v", err)
	}
	return path
}

func TestRunEmbeddedCases(t *testing.T) {
	cases := embeddedCases(t)
	var stdout, stderr bytes.Buffer
	outPath := filepath.Join(t.TempDir(), "report.json")
	if code := run([]string{"-out", outPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("run returned %d, want 0; stderr: %s", code, stderr.String())
	}
	output := stdout.String()
	for _, fragment := range []string{
		"AGENT PIPELINE EVAL",
		"internal/agent/eval/testdata/cases",
		fmt.Sprintf("SUMMARY: %d/%d cases passed", len(cases), len(cases)),
		"PASS",
	} {
		if !strings.Contains(output, fragment) {
			t.Errorf("stdout does not contain %q:\n%s", fragment, output)
		}
	}
	if strings.Contains(output, "FAIL") {
		t.Errorf("stdout reports a failure:\n%s", output)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report eval.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("report is not valid JSON: %v", err)
	}
	if report.Failed != 0 || report.Passed != len(cases) {
		t.Fatalf("report = passed %d, failed %d; want %d/%d", report.Passed, report.Failed, len(cases), len(cases))
	}
}

func TestRunSingleCaseFile(t *testing.T) {
	cases := embeddedCases(t)
	single := cases[0]
	path := writeCase(t, t.TempDir(), "single.json", single)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-cases", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("run returned %d, want 0; stderr: %s", code, stderr.String())
	}
	for _, fragment := range []string{single.Name, "SUMMARY: 1/1 cases passed"} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Errorf("stdout does not contain %q:\n%s", fragment, stdout.String())
		}
	}
}

func TestRunCasesDir(t *testing.T) {
	cases := embeddedCases(t)
	dir := t.TempDir()
	writeCase(t, dir, "a.json", cases[0])
	writeCase(t, dir, "b.json", cases[1])
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-cases", dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("run returned %d, want 0; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "SUMMARY: 2/2 cases passed") {
		t.Errorf("stdout does not contain the two-case summary:\n%s", stdout.String())
	}
}

func TestRunFailingCase(t *testing.T) {
	failing := embeddedCases(t)[0]
	failing.Expect.ModelCalls = func() *int { value := 99; return &value }()
	path := writeCase(t, t.TempDir(), "failing.json", failing)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-cases", path}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run returned %d, want 1; stdout: %s", code, stdout.String())
	}
	for _, fragment := range []string{"FAIL", "FAILURES", "model calls ="} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Errorf("stdout does not contain %q:\n%s", fragment, stdout.String())
		}
	}
	if !strings.Contains(stderr.String(), "cases failed") {
		t.Errorf("stderr = %q, want a case failure message", stderr.String())
	}
}

func TestRunInvalidInputs(t *testing.T) {
	t.Run("missing cases path", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"-cases", filepath.Join(t.TempDir(), "missing")}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("run returned %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "cases path") {
			t.Errorf("stderr = %q, want a cases path error", stderr.String())
		}
	})
	t.Run("empty cases dir", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"-cases", t.TempDir()}, &stdout, &stderr); code != 1 {
			t.Fatalf("run returned %d, want 1", code)
		}
	})
	t.Run("broken fixture", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "broken.json")
		if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if code := run([]string{"-cases", path}, &stdout, &stderr); code != 1 {
			t.Fatalf("run returned %d, want 1", code)
		}
	})
	t.Run("unknown flag", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"-nonsense"}, &stdout, &stderr); code != 2 {
			t.Fatalf("run returned %d, want 2", code)
		}
	})
	t.Run("help", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"-h"}, &stdout, &stderr); code != 0 {
			t.Fatalf("run returned %d, want 0", code)
		}
	})
	t.Run("unwritable report", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		outPath := filepath.Join(t.TempDir(), "missing-dir", "report.json")
		if code := run([]string{"-out", outPath}, &stdout, &stderr); code != 1 {
			t.Fatalf("run returned %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "write") {
			t.Errorf("stderr = %q, want a write error", stderr.String())
		}
	})
}
