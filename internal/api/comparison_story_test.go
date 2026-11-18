package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"activity-descriptor/internal/domain"
)

func storyChangedNode(id, title string, before, after domain.Node, fields ...string) snapshotNodeChange {
	if before.ID == "" {
		before = domain.Node{ID: id, Type: domain.NodeRole, Title: title}
	}
	if after.ID == "" {
		after = domain.Node{ID: id, Type: domain.NodeRole, Title: title}
	}
	return snapshotNodeChange{ID: id, Before: before, After: after, Fields: fields}
}

func TestBuildComparisonStoryEmpty(t *testing.T) {
	for _, test := range []struct {
		name    string
		added   []domain.Node
		removed []domain.Node
		changed []snapshotNodeChange
	}{
		{"nil", nil, nil, nil},
		{"empty slices", []domain.Node{}, []domain.Node{}, []snapshotNodeChange{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			story, lines := buildComparisonStory(test.added, test.removed, test.changed)
			if story != comparisonStoryEmptyText {
				t.Fatalf("story = %q, ожидали %q", story, comparisonStoryEmptyText)
			}
			if len(lines) != 1 || lines[0] != comparisonStoryEmptyText {
				t.Fatalf("story_lines = %#v, ожидали одну строку %q", lines, comparisonStoryEmptyText)
			}
		})
	}
}

func TestBuildComparisonStoryAddedRemovedChanged(t *testing.T) {
	added := []domain.Node{diffTestNode("node-added", "Training", "q-added", domain.EvidencePending)}
	removed := []domain.Node{diffTestNode("node-removed", "Old report", "q-removed", domain.EvidencePending)}
	changed := []snapshotNodeChange{storyChangedNode(
		"node-changed",
		"Reporting",
		domain.Node{ID: "node-changed", Type: domain.NodeRole, Title: "Reporting"},
		domain.Node{ID: "node-changed", Type: domain.NodeRole, Title: "Reporting (new)"},
		"title", "description",
	)}

	story, lines := buildComparisonStory(added, removed, changed)
	wantHeader := "Узлов добавлено: 1, удалено: 1, изменено: 1."
	wantAdded := "Добавлен узел «Training»."
	wantRemoved := "Удалён узел «Old report»."
	wantChanged := "Изменён узел «Reporting (new)»: название, описание."
	wantStory := wantHeader + " " + wantAdded + " " + wantRemoved + " " + wantChanged
	if story != wantStory {
		t.Fatalf("story:\n получили %q\n ожидали %q", story, wantStory)
	}
	wantLines := []string{wantHeader, wantAdded, wantRemoved, wantChanged}
	if !reflect.DeepEqual(lines, wantLines) {
		t.Fatalf("story_lines = %#v, ожидали %#v", lines, wantLines)
	}
}

func TestBuildComparisonStoryCountsMatchDiff(t *testing.T) {
	added := []domain.Node{
		diffTestNode("node-added-1", "A", "q", domain.EvidencePending),
		diffTestNode("node-added-2", "B", "q", domain.EvidencePending),
	}
	removed := []domain.Node{diffTestNode("node-removed", "R", "q", domain.EvidencePending)}
	changed := []snapshotNodeChange{
		storyChangedNode("node-changed-1", "C1", domain.Node{}, domain.Node{}, "title"),
		storyChangedNode("node-changed-2", "C2", domain.Node{}, domain.Node{}, "description"),
		storyChangedNode("node-changed-3", "C3", domain.Node{}, domain.Node{}, "evidence"),
	}
	story, lines := buildComparisonStory(added, removed, changed)
	wantHeader := "Узлов добавлено: 2, удалено: 1, изменено: 3."
	if !strings.HasPrefix(story, wantHeader) {
		t.Fatalf("story должен начинаться с заголовка %q, получили %q", wantHeader, story)
	}
	if lines[0] != wantHeader {
		t.Fatalf("заголовок story_lines = %q, ожидали %q", lines[0], wantHeader)
	}
	// Количества в истории — это len() переданных срезов; никаких других
	// источников счётчиков у истории быть не должно.
	if len(lines)-1 != len(added)+len(removed)+len(changed) {
		t.Fatalf("подробностей %d, ожидали %d", len(lines)-1, len(added)+len(removed)+len(changed))
	}
}

func TestBuildComparisonStoryEvidenceOnlyChange(t *testing.T) {
	modified := domain.Node{ID: "node-1", Type: domain.NodeRole, Title: "Отчёт"}
	changed := []snapshotNodeChange{{
		ID: "node-1", Before: modified, After: modified, Fields: []string{"evidence"},
	}}
	story, lines := buildComparisonStory(nil, nil, changed)
	wantHeader := "Узлов добавлено: 0, удалено: 0, изменено: 1."
	wantDetail := "Изменён узел «Отчёт»: подтверждения."
	if story != wantHeader+" "+wantDetail {
		t.Fatalf("story = %q", story)
	}
	if !reflect.DeepEqual(lines, []string{wantHeader, wantDetail}) {
		t.Fatalf("story_lines = %#v", lines)
	}
	// Пустым diff'ом изменение только подтверждений не считается: это
	// изменение узла, а не отсутствие изменений.
	if story == comparisonStoryEmptyText {
		t.Fatal("evidence-only diff не должен давать пустую историю")
	}
}

func TestBuildComparisonStoryFieldLabelsOrder(t *testing.T) {
	change := storyChangedNode(
		"node-1", "Отчёт", domain.Node{}, domain.Node{},
		// Порядок на входе намеренно перемешан; неизвестные поля — расширение
		// контракта: они идут в конце по алфавиту.
		"evidence", "zun", "title", "custom", "parent_id",
	)
	story, _ := buildComparisonStory(nil, nil, []snapshotNodeChange{change})
	wantDetail := "Изменён узел «Отчёт»: название, родительский узел, подтверждения, custom, zun."
	if !strings.Contains(story, wantDetail) {
		t.Fatalf("story = %q, ожидали фрагмент %q", story, wantDetail)
	}
	// Поле type в истории отсутствует, только если реально изменилось.
	if strings.Contains(story, "тип") {
		t.Fatalf("story не должна упоминать неизменившийся тип: %q", story)
	}
}

func TestBuildComparisonStoryDeterministic(t *testing.T) {
	nodeA := diffTestNode("node-b", "  Training \n node ", "q", domain.EvidencePending)
	nodeB := diffTestNode("node-a", "Reporting", "q", domain.EvidencePending)
	changeA := storyChangedNode("node-z", "Z", domain.Node{}, domain.Node{}, "title", "evidence")
	changeB := storyChangedNode("node-y", "Y", domain.Node{}, domain.Node{}, "description")

	first, firstLines := buildComparisonStory(
		[]domain.Node{nodeA, nodeB}, nil, []snapshotNodeChange{changeA, changeB})
	second, secondLines := buildComparisonStory(
		[]domain.Node{nodeB, nodeA}, nil, []snapshotNodeChange{changeB, changeA})
	if first != second || !reflect.DeepEqual(firstLines, secondLines) {
		t.Fatalf("история не детерминирована:\n%q\n%q", first, second)
	}
	// Добавленные узлы перечисляются по id: node-a, затем node-b.
	indexA := strings.Index(first, "«Reporting»")
	indexB := strings.Index(first, "«Training node»")
	if indexA < 0 || indexB < 0 || indexA > indexB {
		t.Fatalf("порядок добавленных узлов должен быть по id: %q", first)
	}
	// Изменённые узлы тоже по id: node-y, затем node-z.
	indexY := strings.Index(first, "«Y»")
	indexZ := strings.Index(first, "«Z»")
	if indexY < 0 || indexZ < 0 || indexY > indexZ {
		t.Fatalf("порядок изменённых узлов должен быть по id: %q", first)
	}
}

func TestBuildComparisonStoryTitleSanitizing(t *testing.T) {
	longTitle := strings.Repeat("Ж", comparisonStoryTitleLimit+20)
	added := []domain.Node{
		{ID: "node-empty", Type: domain.NodeRole, Title: "  "},
		{ID: "node-long", Type: domain.NodeRole, Title: longTitle},
	}
	story, _ := buildComparisonStory(added, nil, nil)
	if !strings.Contains(story, "Добавлен узел «без названия».") {
		t.Fatalf("пустое название должно заменяться заглушкой: %q", story)
	}
	wantClipped := strings.Repeat("Ж", comparisonStoryTitleLimit) + "…"
	if !strings.Contains(story, "«"+wantClipped+"»") {
		t.Fatalf("длинное название должно обрезаться до %d рун: %q", comparisonStoryTitleLimit, story)
	}
}

func TestBuildComparisonStoryLengthLimits(t *testing.T) {
	// 25 добавленных узлов с длинными названиями: story обязана уложиться в
	// лимит, story_lines — обрезать подробности с пометкой.
	added := make([]domain.Node, 0, 25)
	for index := 0; index < 25; index++ {
		added = append(added, domain.Node{
			ID:    "node-" + strings.Repeat("0", 2) + string(rune('a'+index%26)),
			Type:  domain.NodeRole,
			Title: strings.Repeat("Очень длинное название ", 10),
		})
	}
	story, lines := buildComparisonStory(added, nil, nil)
	if utf8.RuneCountInString(story) > comparisonStoryMaxRunes {
		t.Fatalf("story длиной %d рун превышает лимит %d", utf8.RuneCountInString(story), comparisonStoryMaxRunes)
	}
	if !strings.HasPrefix(story, "Узлов добавлено: 25, удалено: 0, изменено: 0.") {
		t.Fatalf("story потеряла заголовок с количествами: %q", story)
	}
	if !strings.Contains(story, "Ещё изменений: 20.") {
		t.Fatalf("story должна сообщать, сколько изменений не показано: %q", story)
	}
	if len(lines) != 1+comparisonStoryMaxLines+1 {
		t.Fatalf("story_lines: %d строк, ожидали %d", len(lines), 2+comparisonStoryMaxLines)
	}
	if lines[len(lines)-1] != "Ещё изменений: 5." {
		t.Fatalf("последняя строка story_lines = %q, ожидали %q", lines[len(lines)-1], "Ещё изменений: 5.")
	}
}

// TestSnapshotDiffHandlerStoryFields проверяет, что HTTP-ответ snapshot diff
// получил поля story/story_lines, а старые поля не изменились.
func TestSnapshotDiffHandlerStoryFields(t *testing.T) {
	before := domain.Node{ID: "node-1", Type: domain.NodeRole, Title: "Отчёт",
		Evidence: []domain.Evidence{{ID: "ev-1", NodeID: "node-1", SourceID: "src-1", RefID: "msg-1", Quote: "цитата", Status: domain.EvidencePending}}}
	after := domain.Node{ID: "node-1", Type: domain.NodeRole, Title: "Отчёт (уточнено)",
		Evidence: []domain.Evidence{{ID: "ev-1", NodeID: "node-1", SourceID: "src-1", RefID: "msg-1", Quote: "цитата", Status: domain.EvidenceApproved}}}
	stub := &snapshotDiffStub{result: snapshotDiffResponse{
		RunID:   snapshotDiffTestRunID,
		Base:    snapshotVersionRef{ID: snapshotDiffTestBaseID, Version: 1},
		Head:    snapshotVersionRef{ID: snapshotDiffTestHeadID, Version: 2},
		Added:   []domain.Node{diffTestNode("node-added", "Training", "q", domain.EvidencePending)},
		Removed: []domain.Node{},
		Changed: []snapshotNodeChange{{ID: "node-1", Before: before, After: after, Fields: []string{"title", "evidence"}}},
		Summary: snapshotDiffSummary{Added: 1, Removed: 0, Changed: 1},
	}}
	handler := NewServer(stub).Routes()
	rec := do(t, handler, "GET", snapshotDiffTestPath(snapshotDiffTestRunID, snapshotDiffTestHeadID), "teacher", "")
	if rec.Code != 200 {
		t.Fatalf("ожидали 200, получили %d (%s)", rec.Code, rec.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не разобрался: %v", err)
	}
	// Старые поля контракта не сломаны.
	for _, key := range []string{"run_id", "base", "head", "added", "removed", "changed", "summary", "story", "story_lines"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("в ответе нет поля %q: %s", key, rec.Body.String())
		}
	}
	var story string
	if err := json.Unmarshal(body["story"], &story); err != nil {
		t.Fatalf("story не строка: %v", err)
	}
	wantHeader := "Узлов добавлено: 1, удалено: 0, изменено: 1."
	if !strings.HasPrefix(story, wantHeader) {
		t.Fatalf("story = %q, ожидали заголовок %q", story, wantHeader)
	}
	if !strings.Contains(story, "Изменён узел «Отчёт (уточнено)»: название, подтверждения.") {
		t.Fatalf("story не содержит изменённых полей: %q", story)
	}
	var lines []string
	if err := json.Unmarshal(body["story_lines"], &lines); err != nil {
		t.Fatalf("story_lines не массив строк: %v", err)
	}
	if len(lines) != 3 || lines[0] != wantHeader {
		t.Fatalf("story_lines = %#v", lines)
	}
	// Counts истории совпадают с summary ответа.
	var summary snapshotDiffSummary
	if err := json.Unmarshal(body["summary"], &summary); err != nil {
		t.Fatal(err)
	}
	if summary != (snapshotDiffSummary{Added: 1, Removed: 0, Changed: 1}) ||
		!strings.Contains(story, "добавлено: 1") || !strings.Contains(story, "изменено: 1") {
		t.Fatalf("counts истории не совпадают с summary: story=%q summary=%+v", story, summary)
	}
}

// TestSnapshotDiffHandlerStoryEmpty проверяет пустой diff: одинаковые снимки
// дают пустые массивы, нулевой summary и историю «изменений нет».
func TestSnapshotDiffHandlerStoryEmpty(t *testing.T) {
	stub := &snapshotDiffStub{result: snapshotDiffResponse{
		RunID: snapshotDiffTestRunID,
		Base:  snapshotVersionRef{ID: snapshotDiffTestBaseID, Version: 1},
		Head:  snapshotVersionRef{ID: snapshotDiffTestHeadID, Version: 2},
	}}
	handler := NewServer(stub).Routes()
	rec := do(t, handler, "GET", snapshotDiffTestPath(snapshotDiffTestRunID, snapshotDiffTestHeadID), "teacher", "")
	if rec.Code != 200 {
		t.Fatalf("ожидали 200, получили %d (%s)", rec.Code, rec.Body.String())
	}
	var got snapshotDiffResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("ответ не разобрался: %v", err)
	}
	if got.Story != comparisonStoryEmptyText {
		t.Fatalf("story = %q, ожидали %q", got.Story, comparisonStoryEmptyText)
	}
	if !reflect.DeepEqual(got.StoryLines, []string{comparisonStoryEmptyText}) {
		t.Fatalf("story_lines = %#v", got.StoryLines)
	}
	if got.Summary != (snapshotDiffSummary{}) {
		t.Fatalf("summary = %+v", got.Summary)
	}
}
