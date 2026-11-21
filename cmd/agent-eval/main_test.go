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
	"path/filepath"
	"strings"
	"testing"

	"activity-descriptor/internal/agent/eval"
	"activity-descriptor/internal/domain"
)

const referenceExample = "../../internal/agent/eval/testdata/reference/data-analyst.json"

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

func TestParseOptionsReference(t *testing.T) {
	t.Run("все флаги эталона", func(t *testing.T) {
		opts, err := parseOptions([]string{
			"-reference", " ref.json ", "-report", "JSON", "-case", " data-analyst-baseline ",
			"-enforce-thresholds", "-cases", "cases", "-out", "report.json",
		}, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if opts.referencePath != "ref.json" || opts.reportFormat != "json" ||
			opts.caseName != "data-analyst-baseline" || !opts.enforceThresholds {
			t.Fatalf("неверные параметры: %+v", opts)
		}
		if opts.casesPath != "cases" || opts.outPath != "report.json" {
			t.Fatalf("потеряны прежние флаги: %+v", opts)
		}
	})

	t.Run("формат по умолчанию", func(t *testing.T) {
		opts, err := parseOptions([]string{"-reference", "ref.json"}, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if opts.reportFormat != "text" || opts.caseName != "" || opts.enforceThresholds {
			t.Fatalf("неверные параметры по умолчанию: %+v", opts)
		}
	})

	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "report без эталона", args: []string{"-report", "json"}, want: "-reference"},
		{name: "case без эталона", args: []string{"-case", "data-analyst-baseline"}, want: "-reference"},
		{name: "пороги без эталона", args: []string{"-enforce-thresholds"}, want: "-reference"},
		{name: "неизвестный формат", args: []string{"-reference", "ref.json", "-report", "yaml"}, want: "формат"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			_, err := parseOptions(test.args, &stderr)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ожидалась ошибка %q, получено: %v", test.want, err)
			}
			if !strings.Contains(stderr.String(), "agent-eval:") {
				t.Fatalf("ошибка конфигурации не напечатана: %q", stderr.String())
			}
		})
	}

	t.Run("справка", func(t *testing.T) {
		_, err := parseOptions([]string{"-h"}, io.Discard)
		if !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("ожидался flag.ErrHelp, получено: %v", err)
		}
	})
}

func TestReferenceReportExample(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-reference", referenceExample, "-case", "data-analyst-baseline"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run вернул %d; stderr: %s", code, stderr.String())
	}
	for _, fragment := range []string{
		"Сравнение с ручным эталоном",
		"Кейс: data-analyst-baseline",
		"precision: 1.00",
		"recall: 0.50",
		"f1: 0.67",
		"missing (4):",
		"fn-storage",
		"task-adhoc-analysis (не найден)",
		"act-bi-visuals (не найден)",
		"пороги: не выполнены",
	} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("в выводе нет %q:\n%s", fragment, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("без -enforce-thresholds stderr должен быть пуст: %q", stderr.String())
	}
}

func TestReferenceReportEnforceThresholds(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-reference", referenceExample, "-case", "data-analyst-baseline", "-enforce-thresholds"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run вернул %d, ожидался 1", code)
	}
	if !strings.Contains(stdout.String(), "recall: 0.50") {
		t.Fatalf("отчёт не напечатан до кода 1:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "пороги эталона не выполнены") ||
		!strings.Contains(stderr.String(), "recall = 0.50") {
		t.Fatalf("stderr = %q, ожидалось сообщение о recall", stderr.String())
	}
}

func TestReferenceReportJSON(t *testing.T) {
	report := eval.RunAll(context.Background(), embeddedCases(t))
	opts := options{referencePath: referenceExample, reportFormat: "json", caseName: "data-analyst-baseline"}
	var stdout, stderr bytes.Buffer
	if code := reportReference(opts, report, &stdout, &stderr); code != 0 {
		t.Fatalf("reportReference вернул %d; stderr: %s", code, stderr.String())
	}
	var comparison eval.ReferenceReport
	if err := json.Unmarshal(stdout.Bytes(), &comparison); err != nil {
		t.Fatalf("вывод не является JSON: %v\n%s", err, stdout.String())
	}
	if comparison.Precision != 1 || comparison.Recall != 0.5 {
		t.Fatalf("метрики = precision %v, recall %v", comparison.Precision, comparison.Recall)
	}
	if len(comparison.Missing) != 4 || len(comparison.AmbiguousMissing) != 2 || len(comparison.LevelMismatch) != 0 {
		t.Fatalf("неверные списки: %+v", comparison)
	}
}

func TestReferenceThresholdsPass(t *testing.T) {
	path := fullReferenceForBaseline(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-reference", path, "-case", "data-analyst-baseline", "-enforce-thresholds"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run вернул %d; stderr: %s", code, stderr.String())
	}
	for _, fragment := range []string{"recall: 1.00", "missing: нет", "пороги: выполнены"} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("в выводе нет %q:\n%s", fragment, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr должен быть пуст: %q", stderr.String())
	}
}

// fullReferenceForBaseline создаёт эталон, полностью совпадающий с деревом
// кейса data-analyst-baseline. Он нужен только для проверки прохождения порогов.
func fullReferenceForBaseline(t *testing.T) string {
	t.Helper()
	source := eval.ReferenceSource{
		SourceID: "src-interview", SourceType: domain.SourceInterview, Quote: "цитата интервью",
	}
	node := func(id string, nodeType domain.NodeType, parentID, title string) eval.ReferenceNode {
		return eval.ReferenceNode{
			ID: id, Type: nodeType, ParentID: parentID, Title: title,
			Sources: []eval.ReferenceSource{source},
		}
	}
	ref := eval.Reference{
		Profession: "Аналитик данных",
		Version:    "test",
		Nodes: []eval.ReferenceNode{
			node("role", domain.NodeRole, "", "Аналитик данных"),
			node("fn", domain.NodeFunction, "role", "Отчётность и визуализация"),
			node("task", domain.NodeTask, "fn", "Писать SQL-запросы"),
			node("act", domain.NodeAction, "task", "Оптимизировать SQL-запросы"),
		},
	}
	data, err := json.MarshalIndent(ref, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "full-reference.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReferenceReportConfigErrors(t *testing.T) {
	broken := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(broken, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := map[string]struct {
		args []string
		want string
	}{
		"битый эталон": {
			args: []string{"-reference", broken, "-case", "data-analyst-baseline"},
			want: "разбор эталона",
		},
		"эталон отсутствует": {
			args: []string{"-reference", filepath.Join(t.TempDir(), "missing.json"), "-case", "data-analyst-baseline"},
			want: "чтение эталона",
		},
		"неизвестный кейс": {
			args: []string{"-reference", referenceExample, "-case", "no-such-case"},
			want: "не найден",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(test.args, &stdout, &stderr); code != 2 {
				t.Fatalf("run вернул %d, ожидался 2", code)
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr = %q, ожидалось %q", stderr.String(), test.want)
			}
		})
	}
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
