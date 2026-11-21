package eval

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"activity-descriptor/internal/domain"
)

func TestGeneratedTree(t *testing.T) {
	parent := "role-1"
	nodes := []domain.Node{
		{ID: "role-1", Type: domain.NodeRole, Title: "Роль"},
		{ID: "fn-1", Type: domain.NodeFunction, ParentID: &parent, Title: "Функция"},
	}
	generated := GeneratedTree(nodes)
	if len(generated) != 2 {
		t.Fatalf("узлов = %d, ожидалось 2", len(generated))
	}
	if generated[0].ParentID != "" {
		t.Fatalf("у корня ParentID = %q, ожидалась пустая строка", generated[0].ParentID)
	}
	if generated[1].ParentID != "role-1" || generated[1].Type != domain.NodeFunction {
		t.Fatalf("дочерний узел преобразован неверно: %+v", generated[1])
	}
	if got := GeneratedTree(nil); got != nil {
		t.Fatalf("для пустого дерева ожидался nil, получено %#v", got)
	}
}

func TestRunCaseCapturesTreeOutsideJSON(t *testing.T) {
	report := RunCase(context.Background(), caseByName(t, "data-analyst-baseline"))
	if len(report.Tree) != 4 {
		t.Fatalf("дерево кейса = %d узлов, ожидалось 4", len(report.Tree))
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"Tree"`) || strings.Contains(string(data), `"tree":`) {
		t.Fatalf("дерево не должно попадать в JSON-отчёт:\n%s", data)
	}
	if !strings.Contains(string(data), `"tree_nodes":4`) {
		t.Fatalf("метрика tree_nodes должна сохраниться:\n%s", data)
	}
}

// TestCompareTreesExampleReference проверяет связку реального кейса с примером
// формата эталона. Пример демонстрационный, поэтому recall 0.50 ожидаем: в нём
// есть узлы, которых нет в дереве baseline. Тест фиксирует именно эту границу,
// а не выдаёт пример за согласованный пилотный эталон.
func TestCompareTreesExampleReference(t *testing.T) {
	ref, err := LoadReference(filepath.Join("testdata", "reference", "data-analyst.json"))
	if err != nil {
		t.Fatalf("LoadReference: %v", err)
	}
	report := RunCase(context.Background(), caseByName(t, "data-analyst-baseline"))
	if len(report.Tree) == 0 {
		t.Fatal("у кейса нет дерева для сравнения")
	}

	comparison := CompareTrees(ref, report.Tree)
	if comparison.Error != "" {
		t.Fatalf("сравнение: %s", comparison.Error)
	}
	assertMetric(t, "precision", comparison.Precision, 1)
	assertMetric(t, "recall", comparison.Recall, 0.5)
	if math.Abs(comparison.F1-2.0/3.0) > 1e-9 {
		t.Fatalf("f1 = %v, ожидалось %v", comparison.F1, 2.0/3.0)
	}
	if len(comparison.Matched) != 4 || len(comparison.Extra) != 0 || len(comparison.LevelMismatch) != 0 {
		t.Fatalf("неверные списки сравнения: %+v", comparison)
	}
	for _, want := range []string{"fn-storage", "fn-decision-support", "task-regular-reports", "task-dashboards"} {
		if !containsString(comparison.Missing, want) {
			t.Errorf("missing = %v, нет обязательного узла %q", comparison.Missing, want)
		}
	}
	for _, want := range []string{"task-adhoc-analysis", "act-bi-visuals"} {
		if !containsString(comparison.AmbiguousMissing, want) {
			t.Errorf("ambiguous missing = %v, нет спорного узла %q", comparison.AmbiguousMissing, want)
		}
	}

	violations := CheckReferenceThresholds(comparison)
	if len(violations) != 1 || !strings.Contains(violations[0], "recall") {
		t.Fatalf("ожидалось только нарушение recall, получено: %v", violations)
	}
}

func TestCheckReferenceThresholds(t *testing.T) {
	tests := map[string]struct {
		report ReferenceReport
		want   []string
	}{
		"полное совпадение": {
			report: ReferenceReport{Precision: 1, Recall: 1, F1: 1},
		},
		"recall ровно на пороге": {
			report: ReferenceReport{Precision: 0.9, Recall: 0.8, F1: 0.85},
		},
		"recall ниже порога": {
			report: ReferenceReport{Precision: 0.9, Recall: 0.79, F1: 0.84},
			want:   []string{"recall"},
		},
		"нарушен уровень": {
			report: ReferenceReport{Precision: 1, Recall: 1, LevelMismatch: []string{"act: ожидался action, получен task"}},
			want:   []string{"level_mismatch"},
		},
		"ошибка сравнения": {
			report: ReferenceReport{Error: "эталон пуст"},
			want:   []string{"сравнение"},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			violations := CheckReferenceThresholds(test.report)
			if len(violations) != len(test.want) {
				t.Fatalf("нарушений = %d (%v), ожидалось %d", len(violations), violations, len(test.want))
			}
			for i, want := range test.want {
				if !strings.Contains(violations[i], want) {
					t.Fatalf("нарушение %q не содержит %q", violations[i], want)
				}
			}
		})
	}
}

func TestFormatReferenceReport(t *testing.T) {
	ref := &Reference{
		Profession: "Аналитик данных",
		Version:    "2",
		ReviewedBy: "методист",
		ReviewedAt: "2026-11-20",
		Nodes: []ReferenceNode{
			referenceNode("role", domain.NodeRole, "", "Аналитик данных"),
			referenceNode("fn", domain.NodeFunction, "role", "Отчётность"),
			{
				ID: "amb", Type: domain.NodeTask, ParentID: "fn", Title: "Проводить ad-hoc анализ",
				Ambiguous: true, Note: "граница задачи требует согласования",
				Sources: []ReferenceSource{referenceSource()},
			},
		},
	}
	report := ReferenceReport{
		Matched: []string{"role"}, Missing: []string{"fn"},
		AmbiguousMissing: []string{"amb"},
		Precision:        1,
		Recall:           0.5,
		F1:               2.0 / 3.0,
	}
	text := FormatReferenceReport("data-analyst-baseline", ref, report)
	for _, want := range []string{
		"Сравнение с ручным эталоном",
		"Кейс: data-analyst-baseline",
		"Эталон: «Аналитик данных», версия 2, проверил: методист, дата: 2026-11-20",
		"precision: 1.00",
		"recall: 0.50",
		"f1: 0.67",
		"matched: 1",
		"missing (1): fn",
		"extra: нет",
		"level_mismatch: нет",
		"спорные узлы:",
		"amb (не найден): Проводить ad-hoc анализ — граница задачи требует согласования",
		"пороги: не выполнены",
		"recall = 0.50, нужно >= 0.80",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("в отчёте нет %q:\n%s", want, text)
		}
	}

	full := FormatReferenceReport("case", ref, ReferenceReport{Matched: []string{"role", "fn", "amb"}, Precision: 1, Recall: 1, F1: 1})
	if !strings.Contains(full, "amb (найден)") {
		t.Fatalf("найденный спорный узел не подписан:\n%s", full)
	}
	if !strings.Contains(full, "пороги: выполнены") {
		t.Fatalf("у полного совпадения пороги должны быть выполнены:\n%s", full)
	}

	mismatch := FormatReferenceReport("case", ref, ReferenceReport{
		LevelMismatch: []string{"amb: ожидался task, получен action"},
	})
	if !strings.Contains(mismatch, "amb (нарушен уровень)") {
		t.Fatalf("нарушение уровня у спорного узла не показано:\n%s", mismatch)
	}
}

func TestFormatReferenceReportError(t *testing.T) {
	text := FormatReferenceReport("case", nil, ReferenceReport{Error: "эталон не передан"})
	if !strings.Contains(text, "Ошибка: эталон не передан") {
		t.Fatalf("ошибка сравнения не напечатана:\n%s", text)
	}
}
