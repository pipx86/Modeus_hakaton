package eval

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"activity-descriptor/internal/domain"
)

func referenceSource() ReferenceSource {
	return ReferenceSource{
		SourceID:   "src-interview",
		SourceType: domain.SourceInterview,
		Quote:      "цитата из интервью",
		Reason:     "ответ респондента",
	}
}

func referenceNode(id string, nodeType domain.NodeType, parentID, title string) ReferenceNode {
	return ReferenceNode{
		ID:       id,
		Type:     nodeType,
		ParentID: parentID,
		Title:    title,
		Sources:  []ReferenceSource{referenceSource()},
	}
}

func referenceTree() []ReferenceNode {
	return []ReferenceNode{
		referenceNode("role", domain.NodeRole, "", "Аналитик данных"),
		referenceNode("fn", domain.NodeFunction, "role", "Отчётность"),
		referenceNode("task", domain.NodeTask, "fn", "Готовить отчёты"),
		referenceNode("act", domain.NodeAction, "task", "Проверять выгрузку"),
	}
}

func validReference() *Reference {
	return &Reference{Profession: "Аналитик данных", Version: "1", Nodes: referenceTree()}
}

func generatedTree() []GeneratedNode {
	return []GeneratedNode{
		{ID: "g-role", Type: domain.NodeRole, Title: "Аналитик данных"},
		{ID: "g-fn", Type: domain.NodeFunction, ParentID: "g-role", Title: "Отчётность"},
		{ID: "g-task", Type: domain.NodeTask, ParentID: "g-fn", Title: "Готовить отчёты"},
		{ID: "g-act", Type: domain.NodeAction, ParentID: "g-task", Title: "Проверять выгрузку"},
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func assertMetric(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestReferenceValidateHappyPath(t *testing.T) {
	ref := validReference()
	if err := ref.Validate(); err != nil {
		t.Fatalf("valid reference rejected: %v", err)
	}
	if len(ref.Nodes) != 4 {
		t.Fatalf("sample reference has %d nodes, want 4", len(ref.Nodes))
	}
}

func TestReferenceValidateRejectsBrokenTrees(t *testing.T) {
	tests := map[string]struct {
		nodes []ReferenceNode
		want  string
	}{
		"два корня": {
			nodes: []ReferenceNode{
				referenceNode("role", domain.NodeRole, "", "Роль"),
				referenceNode("fn", domain.NodeFunction, "", "Функция"),
			},
			want: "ровно один корень",
		},
		"нет корня": {
			nodes: []ReferenceNode{
				referenceNode("a", domain.NodeAction, "b", "Действие A"),
				referenceNode("b", domain.NodeAction, "a", "Действие B"),
			},
			want: "нет корня",
		},
		"ребёнок-сирота": {
			nodes: []ReferenceNode{
				referenceNode("role", domain.NodeRole, "", "Роль"),
				referenceNode("fn", domain.NodeFunction, "ghost", "Функция"),
			},
			want: "несуществующего родителя",
		},
		"узел без источника": {
			nodes: []ReferenceNode{
				func() ReferenceNode {
					node := referenceNode("role", domain.NodeRole, "", "Роль")
					node.Sources = nil
					return node
				}(),
			},
			want: "без источников",
		},
		"узел без цитаты": {
			nodes: []ReferenceNode{
				func() ReferenceNode {
					node := referenceNode("role", domain.NodeRole, "", "Роль")
					node.Sources[0].Quote = "   "
					return node
				}(),
			},
			want: "цитаты",
		},
		"пустой source_id": {
			nodes: []ReferenceNode{
				func() ReferenceNode {
					node := referenceNode("role", domain.NodeRole, "", "Роль")
					node.Sources[0].SourceID = " "
					return node
				}(),
			},
			want: "пустой source_id",
		},
		"неизвестный source_type": {
			nodes: []ReferenceNode{
				func() ReferenceNode {
					node := referenceNode("role", domain.NodeRole, "", "Роль")
					node.Sources[0].SourceType = domain.SourceType("hr")
					return node
				}(),
			},
			want: "неизвестный source_type",
		},
		"узел без id": {
			nodes: []ReferenceNode{referenceNode("", domain.NodeRole, "", "Роль")},
			want:  "без id",
		},
		"повторяющийся id": {
			nodes: []ReferenceNode{
				referenceNode("same", domain.NodeRole, "", "Роль"),
				referenceNode("same", domain.NodeFunction, "same", "Функция"),
			},
			want: "повторяющийся id",
		},
		"узел без названия": {
			nodes: []ReferenceNode{referenceNode("role", domain.NodeRole, "", "  ")},
			want:  "без названия",
		},
		"неизвестный тип": {
			nodes: []ReferenceNode{referenceNode("role", domain.NodeType("department"), "", "Роль")},
			want:  "неизвестный тип",
		},
		"нарушен уровень": {
			nodes: []ReferenceNode{
				referenceNode("role", domain.NodeRole, "", "Роль"),
				referenceNode("task", domain.NodeTask, "role", "Задача"),
			},
			want: "не может быть дочерним",
		},
		"корень не role": {
			nodes: []ReferenceNode{referenceNode("fn", domain.NodeFunction, "", "Функция")},
			want:  "должен иметь тип role",
		},
		"дубль названий у одного родителя": {
			nodes: []ReferenceNode{
				referenceNode("role", domain.NodeRole, "", "Роль"),
				referenceNode("fn-1", domain.NodeFunction, "role", "Отчётность"),
				referenceNode("fn-2", domain.NodeFunction, "role", "  отчётность  "),
			},
			want: "одинаковым названием",
		},
		"цикл вне корня": {
			nodes: []ReferenceNode{
				referenceNode("role", domain.NodeRole, "", "Роль"),
				referenceNode("a", domain.NodeAction, "b", "Действие A"),
				referenceNode("b", domain.NodeAction, "a", "Действие B"),
			},
			want: "не достижим от корня",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ref := &Reference{Profession: "Аналитик данных", Version: "1", Nodes: test.nodes}
			err := ref.Validate()
			if err == nil {
				t.Fatalf("want validation error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %q does not contain %q", err, test.want)
			}
		})
	}
}

func TestReferenceValidateTopLevel(t *testing.T) {
	ref := validReference()
	ref.Profession = "  "
	if err := ref.Validate(); err == nil || !strings.Contains(err.Error(), "профессия") {
		t.Fatalf("empty profession: err = %v", err)
	}

	ref = validReference()
	ref.Version = " "
	if err := ref.Validate(); err == nil || !strings.Contains(err.Error(), "версия") {
		t.Fatalf("empty version: err = %v", err)
	}

	ref = validReference()
	ref.Nodes = nil
	if err := ref.Validate(); err == nil || !strings.Contains(err.Error(), "нет узлов") {
		t.Fatalf("empty nodes: err = %v", err)
	}

	var nilRef *Reference
	if err := nilRef.Validate(); err == nil || !strings.Contains(err.Error(), "не передан") {
		t.Fatalf("nil reference: err = %v", err)
	}
}

func TestLoadReferenceExample(t *testing.T) {
	ref, err := LoadReference(filepath.Join("testdata", "reference", "data-analyst.json"))
	if err != nil {
		t.Fatalf("LoadReference example: %v", err)
	}
	if ref.Profession != "Аналитик данных" {
		t.Errorf("profession = %q, want Аналитик данных", ref.Profession)
	}
	if strings.TrimSpace(ref.Version) == "" {
		t.Error("example has empty version")
	}
	if !strings.Contains(strings.ToLower(ref.Note), "не пилотный эталон") {
		t.Errorf("example note %q does not mark it as a non-pilot sample", ref.Note)
	}
	if total := len(ref.Nodes); total < 8 || total > 10 {
		t.Errorf("example has %d nodes, want 8..10", total)
	}

	counts := make(map[domain.NodeType]int)
	roots, ambiguous := 0, 0
	for _, node := range ref.Nodes {
		counts[node.Type]++
		if strings.TrimSpace(node.ParentID) == "" {
			roots++
		}
		if node.Ambiguous {
			ambiguous++
			if strings.TrimSpace(node.Note) == "" {
				t.Errorf("ambiguous node %q has no note", node.ID)
			}
		}
		hasQuote := false
		for _, source := range node.Sources {
			if strings.TrimSpace(source.Quote) != "" {
				hasQuote = true
			}
		}
		if !hasQuote {
			t.Errorf("node %q has no quoted source", node.ID)
		}
	}
	if roots != 1 {
		t.Errorf("example has %d roots, want exactly 1", roots)
	}
	if counts[domain.NodeRole] != 1 {
		t.Errorf("example has %d role nodes, want 1", counts[domain.NodeRole])
	}
	if got := counts[domain.NodeFunction]; got < 3 || got > 5 {
		t.Errorf("example has %d functions, want 3..5", got)
	}
	if got := counts[domain.NodeTask]; got < 3 || got > 4 {
		t.Errorf("example has %d tasks, want 3..4", got)
	}
	if got := counts[domain.NodeAction]; got < 2 || got > 3 {
		t.Errorf("example has %d actions, want 2..3", got)
	}
	if ambiguous < 1 || ambiguous > 2 {
		t.Errorf("example has %d ambiguous nodes, want 1..2", ambiguous)
	}
}

func TestLoadReferenceRejectsBadFiles(t *testing.T) {
	if _, err := LoadReference(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("missing file: want error")
	}

	dir := t.TempDir()
	malformed := filepath.Join(dir, "malformed.json")
	if err := os.WriteFile(malformed, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReference(malformed); err == nil || !strings.Contains(err.Error(), "разбор эталона") {
		t.Fatalf("malformed JSON: err = %v", err)
	}

	unknownField := filepath.Join(dir, "unknown.json")
	document := `{
		"profession": "Аналитик данных",
		"version": "1",
		"nodes": [{
			"id": "role",
			"type": "role",
			"title": "Аналитик данных",
			"sources": [{"source_id": "s", "source_type": "interview", "quote": "цитата"}]
		}],
		"surprise": true
	}`
	if err := os.WriteFile(unknownField, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReference(unknownField); err == nil {
		t.Fatal("unknown field: want error")
	}

	broken := validReference()
	broken.Nodes = append(broken.Nodes, referenceNode("fn-2", domain.NodeFunction, "", "Ещё функция"))
	data, err := json.Marshal(broken)
	if err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(dir, "invalid.json")
	if err := os.WriteFile(invalid, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReference(invalid); err == nil || !strings.Contains(err.Error(), "корень") {
		t.Fatalf("invalid reference: err = %v", err)
	}
}

func TestCompareTreesFullMatch(t *testing.T) {
	report := CompareTrees(validReference(), generatedTree())
	if report.Error != "" {
		t.Fatalf("unexpected error: %s", report.Error)
	}
	assertMetric(t, "precision", report.Precision, 1)
	assertMetric(t, "recall", report.Recall, 1)
	assertMetric(t, "f1", report.F1, 1)
	if len(report.Matched) != 4 {
		t.Fatalf("matched = %v, want all 4 reference ids", report.Matched)
	}
	if len(report.Missing) != 0 || len(report.Extra) != 0 ||
		len(report.LevelMismatch) != 0 || len(report.AmbiguousMissing) != 0 {
		t.Fatalf("unexpected differences: %+v", report)
	}
}

// TestCompareTreesPipelineBaseline проверяет полный путь: дерево реального
// пайплайна → GeneratedNode → сравнение с эталоном. Это точка подключения
// ручного эталона к eval-корпусу.
func TestCompareTreesPipelineBaseline(t *testing.T) {
	result := runOne(t, "data-analyst-baseline")
	ref := &Reference{Profession: "Аналитик данных", Version: "test", Nodes: []ReferenceNode{
		referenceNode("role", domain.NodeRole, "", "Аналитик данных"),
		referenceNode("fn", domain.NodeFunction, "role", "Отчётность и визуализация"),
		referenceNode("task", domain.NodeTask, "fn", "Писать SQL-запросы"),
		referenceNode("act", domain.NodeAction, "task", "Оптимизировать SQL-запросы"),
	}}
	generated := make([]GeneratedNode, 0, len(result.Synthesis.Tree))
	for _, node := range result.Synthesis.Tree {
		parentID := ""
		if node.ParentID != nil {
			parentID = *node.ParentID
		}
		generated = append(generated, GeneratedNode{
			ID: node.ID, Type: node.Type, ParentID: parentID, Title: node.Title,
		})
	}
	report := CompareTrees(ref, generated)
	if report.Error != "" {
		t.Fatalf("сравнение: %s", report.Error)
	}
	assertMetric(t, "precision", report.Precision, 1)
	assertMetric(t, "recall", report.Recall, 1)
	if len(report.LevelMismatch) != 0 || len(report.Missing) != 0 || len(report.Extra) != 0 {
		t.Fatalf("эталон разошёлся с пайплайном: %+v", report)
	}
}

func TestCompareTreesMissingNode(t *testing.T) {
	report := CompareTrees(validReference(), generatedTree()[:3])
	assertMetric(t, "precision", report.Precision, 1)
	assertMetric(t, "recall", report.Recall, 0.75)
	if report.F1 >= 1 {
		t.Errorf("f1 = %v, want below 1 for a missing node", report.F1)
	}
	if !containsString(report.Missing, "act") {
		t.Fatalf("missing = %v, want act", report.Missing)
	}
	if len(report.Extra) != 0 || len(report.LevelMismatch) != 0 {
		t.Fatalf("unexpected differences: %+v", report)
	}
}

func TestCompareTreesExtraNode(t *testing.T) {
	generated := append(generatedTree(), GeneratedNode{
		ID: "g-extra", Type: domain.NodeTask, ParentID: "g-missing", Title: "Лишняя задача",
	})
	report := CompareTrees(validReference(), generated)
	assertMetric(t, "precision", report.Precision, 0.8)
	assertMetric(t, "recall", report.Recall, 1)
	if !containsString(report.Extra, "g-extra") {
		t.Fatalf("extra = %v, want g-extra", report.Extra)
	}
	if len(report.Missing) != 0 || len(report.LevelMismatch) != 0 {
		t.Fatalf("unexpected differences: %+v", report)
	}
}

func TestCompareTreesLevelMismatch(t *testing.T) {
	generated := generatedTree()
	generated[3].Type = domain.NodeTask
	report := CompareTrees(validReference(), generated)
	assertMetric(t, "precision", report.Precision, 0.75)
	assertMetric(t, "recall", report.Recall, 0.75)
	assertMetric(t, "f1", report.F1, 0.75)
	if len(report.LevelMismatch) != 1 || !strings.Contains(report.LevelMismatch[0], "act") {
		t.Fatalf("level mismatch = %v, want one entry for act", report.LevelMismatch)
	}
	if !strings.Contains(report.LevelMismatch[0], "ожидался action") ||
		!strings.Contains(report.LevelMismatch[0], "получен task") {
		t.Fatalf("level mismatch text = %q, want expected/actual types", report.LevelMismatch[0])
	}
	if len(report.Missing) != 0 || len(report.Extra) != 0 {
		t.Fatalf("a type mismatch must not be reported as missing/extra: %+v", report)
	}
}

func TestCompareTreesAmbiguousNode(t *testing.T) {
	ref := validReference()
	ref.Nodes = append(ref.Nodes, ReferenceNode{
		ID: "act-amb", Type: domain.NodeAction, ParentID: "task", Title: "Строить дашборд",
		Ambiguous: true, Note: "спорное действие", Sources: []ReferenceSource{referenceSource()},
	})

	withoutNode := CompareTrees(ref, generatedTree())
	assertMetric(t, "precision", withoutNode.Precision, 1)
	assertMetric(t, "recall", withoutNode.Recall, 1)
	if !containsString(withoutNode.AmbiguousMissing, "act-amb") {
		t.Fatalf("ambiguous missing = %v, want act-amb", withoutNode.AmbiguousMissing)
	}
	if len(withoutNode.Missing) != 0 {
		t.Fatalf("missing = %v, an ambiguous node must not be a hard failure", withoutNode.Missing)
	}

	withNode := CompareTrees(ref, append(generatedTree(), GeneratedNode{
		ID: "g-amb", Type: domain.NodeAction, ParentID: "g-task", Title: "Строить дашборд",
	}))
	assertMetric(t, "precision", withNode.Precision, 1)
	assertMetric(t, "recall", withNode.Recall, 1)
	if len(withNode.AmbiguousMissing) != 0 || !containsString(withNode.Matched, "act-amb") {
		t.Fatalf("ambiguous node should be matched, report: %+v", withNode)
	}
	if containsString(withNode.Extra, "g-amb") {
		t.Fatalf("generated ambiguous node counted as extra: %v", withNode.Extra)
	}
}

func TestCompareTreesEmptyReference(t *testing.T) {
	tests := map[string]*Reference{
		"nil":             nil,
		"empty":           {},
		"without nodes":   {Profession: "Аналитик данных"},
		"only profession": {Profession: "Аналитик данных", Version: "1"},
	}
	for name, ref := range tests {
		t.Run(name, func(t *testing.T) {
			report := CompareTrees(ref, generatedTree())
			if report.Error == "" {
				t.Fatal("want an error for an empty reference")
			}
			assertMetric(t, "precision", report.Precision, 0)
			assertMetric(t, "recall", report.Recall, 0)
			assertMetric(t, "f1", report.F1, 0)
		})
	}
}

func TestCompareTreesEmptyGenerated(t *testing.T) {
	report := CompareTrees(validReference(), nil)
	assertMetric(t, "precision", report.Precision, 0)
	assertMetric(t, "recall", report.Recall, 0)
	assertMetric(t, "f1", report.F1, 0)
	if len(report.Missing) != 4 {
		t.Fatalf("missing = %v, want all 4 reference nodes", report.Missing)
	}
	if len(report.Extra) != 0 {
		t.Fatalf("extra = %v, want none", report.Extra)
	}
}

func TestCompareTreesAllAmbiguous(t *testing.T) {
	ref := &Reference{Profession: "Аналитик данных", Nodes: []ReferenceNode{{
		ID: "role", Type: domain.NodeRole, Title: "Аналитик данных",
		Ambiguous: true, Note: "спорная роль", Sources: []ReferenceSource{referenceSource()},
	}}}
	report := CompareTrees(ref, []GeneratedNode{{ID: "g-role", Type: domain.NodeRole, Title: "Аналитик данных"}})
	if !containsString(report.Matched, "role") {
		t.Fatalf("matched = %v, want role", report.Matched)
	}
	assertMetric(t, "precision", report.Precision, 0)
	assertMetric(t, "recall", report.Recall, 0)
	assertMetric(t, "f1", report.F1, 0)
}

func TestCompareTreesNormalizesTitles(t *testing.T) {
	ref := &Reference{Profession: "Аналитик данных", Nodes: []ReferenceNode{
		referenceNode("r", domain.NodeRole, "", "Аналитик данных"),
		referenceNode("f", domain.NodeFunction, "r", "Писать SQL-запросы"),
		referenceNode("t", domain.NodeTask, "f", "Учёт требований"),
	}}
	generated := []GeneratedNode{
		{ID: "g3", Type: domain.NodeTask, ParentID: "g2", Title: "  учет   требований  "},
		{ID: "g1", Type: domain.NodeRole, Title: " аналитик   данных "},
		{ID: "g2", Type: domain.NodeFunction, ParentID: "g1", Title: "писать sql-запросы"},
	}
	report := CompareTrees(ref, generated)
	if report.Error != "" {
		t.Fatalf("unexpected error: %s", report.Error)
	}
	assertMetric(t, "precision", report.Precision, 1)
	assertMetric(t, "recall", report.Recall, 1)
	assertMetric(t, "f1", report.F1, 1)
	if len(report.Matched) != 3 {
		t.Fatalf("matched = %v, want all 3 reference ids", report.Matched)
	}
}

func TestNormalizeReferenceTitle(t *testing.T) {
	tests := map[string]string{
		"  Писать   SQL-запросы  ": "писать sql-запросы",
		"Учёт метрик":              "учет метрик",
		"\tЁмкость\n":              "емкость",
		"":                         "",
	}
	for input, want := range tests {
		if got := normalizeReferenceTitle(input); got != want {
			t.Errorf("normalizeReferenceTitle(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCompareTreesUsesParentForDuplicateTitles(t *testing.T) {
	ref := &Reference{Profession: "Аналитик данных", Nodes: []ReferenceNode{
		referenceNode("r", domain.NodeRole, "", "Аналитик данных"),
		referenceNode("f1", domain.NodeFunction, "r", "Отчётность"),
		referenceNode("f2", domain.NodeFunction, "r", "Аналитика"),
		referenceNode("a", domain.NodeTask, "f1", "Готовить отчёт"),
		referenceNode("b", domain.NodeTask, "f2", "Готовить отчёт"),
	}}
	// Дубль названия под другим родителем идёт первым: выбор должен опираться
	// на родителя, а не на порядок узлов в генерации.
	generated := []GeneratedNode{
		{ID: "g-r", Type: domain.NodeRole, Title: "Аналитик данных"},
		{ID: "g-f1", Type: domain.NodeFunction, ParentID: "g-r", Title: "Отчётность"},
		{ID: "g-f2", Type: domain.NodeFunction, ParentID: "g-r", Title: "Аналитика"},
		{ID: "g-b", Type: domain.NodeTask, ParentID: "g-f2", Title: "Готовить отчёт"},
		{ID: "g-a", Type: domain.NodeAction, ParentID: "g-f1", Title: "Готовить отчёт"},
	}
	report := CompareTrees(ref, generated)
	if !containsString(report.Matched, "b") {
		t.Fatalf("matched = %v, want b matched under its own parent", report.Matched)
	}
	if len(report.LevelMismatch) != 1 || !strings.Contains(report.LevelMismatch[0], "a") {
		t.Fatalf("level mismatch = %v, want a (same parent, wrong type)", report.LevelMismatch)
	}
	assertMetric(t, "precision", report.Precision, 0.8)
	assertMetric(t, "recall", report.Recall, 0.8)
	if len(report.Missing) != 0 || len(report.Extra) != 0 {
		t.Fatalf("unexpected missing/extra: %+v", report)
	}
}
