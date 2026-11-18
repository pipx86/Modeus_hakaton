package api

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"activity-descriptor/internal/domain"
)

// --- semantic matching helpers ---

func matchTestEvidence(id, sourceID, refID, quote string, status domain.EvidenceStatus) domain.Evidence {
	return domain.Evidence{ID: id, SourceID: sourceID, RefID: refID, Quote: quote, Status: status}
}

func matchTestNode(id string, nodeType domain.NodeType, title string, parentID *string, description string, evidence ...domain.Evidence) domain.Node {
	return domain.Node{ID: id, Type: nodeType, Title: title, ParentID: parentID, Description: description, Evidence: evidence}
}

func ptrTo(value string) *string { return &value }

// semanticTree строит дерево role → function → task с префиксом id и общим
// содержимым. Одинаковые аргументы с разными prefix/sourceID дают деревья,
// которые независимые прогоны сохраняют как разное UUID-содержимое.
func semanticTree(prefix, sourceID string) []domain.Node {
	root := prefix + "-root"
	function := prefix + "-function"
	task := prefix + "-task"
	return []domain.Node{
		matchTestNode(root, domain.NodeRole, "Аналитик данных", nil, "",
			matchTestEvidence(prefix+"-ev-root", sourceID, "msg-role", "цитата роли", domain.EvidencePending)),
		matchTestNode(function, domain.NodeFunction, "Отчётность", ptrTo(root), "Готовить отчёты",
			matchTestEvidence(prefix+"-ev-function", sourceID, "msg-function", "цитата функции", domain.EvidencePending)),
		matchTestNode(task, domain.NodeTask, "Писать SQL-запросы", ptrTo(function), "",
			matchTestEvidence(prefix+"-ev-task", sourceID, "msg-task", "цитата задачи", domain.EvidencePending)),
	}
}

func TestRunCompareNormalizeText(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"  Писать   SQL-запросы.  ", "писать sql-запросы"},
		{"Отчётность", "отчетность"},
		{"отчетность", "отчетность"},
		{"Ёлка!", "елка"},
		{"Текст?..»", "текст"},
		{"", ""},
	}
	for _, test := range tests {
		if got := runCompareNormalizeText(test.in); got != test.want {
			t.Fatalf("normalize(%q) = %q, ожидали %q", test.in, got, test.want)
		}
	}
}

func TestRunCompareSameProfession(t *testing.T) {
	rootA := []domain.Node{matchTestNode("a-root", domain.NodeRole, "Аналитик данных", nil, "")}
	rootB := []domain.Node{matchTestNode("b-root", domain.NodeRole, "Аналитик данных", nil, "")}
	rootDifferent := []domain.Node{matchTestNode("c-root", domain.NodeRole, "DevOps Engineer", nil, "")}
	tests := []struct {
		name        string
		profession  string
		againstProf string
		headNodes   []domain.Node
		baseNodes   []domain.Node
		want        bool
	}{
		{"same profession", "Data Analyst", "Data Analyst", rootA, rootDifferent, true},
		{"normalized profession", "  data   ANALYST ", "Data Analyst", rootA, rootDifferent, true},
		{"different profession, same root title", "Аналитик данных", "Data Analyst", rootA, rootB, true},
		{"different profession, different root", "Data Analyst", "DevOps Engineer", rootA, rootDifferent, false},
		{"no trees, same profession", "Data Analyst", "Data Analyst", nil, nil, true},
		{"no trees, different profession", "Data Analyst", "DevOps Engineer", nil, nil, false},
		{"empty profession fields, different roots", "", "", rootA, rootDifferent, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := runCompareSameProfession(
				&domain.Run{ID: compareTestRunID, Profession: test.profession},
				&domain.Run{ID: compareTestAgainstID, Profession: test.againstProf},
				test.headNodes, test.baseNodes)
			if got != test.want {
				t.Fatalf("sameProfession = %v, ожидали %v", got, test.want)
			}
		})
	}
}

func TestBuildRunCompareResultSemanticIdentical(t *testing.T) {
	// Независимые прогоны: одинаковое содержимое, но разные UUID узлов,
	// подтверждений и источников.
	base := semanticTree("base", "source-base")
	head := semanticTree("head", "source-head")
	result := buildRunCompareResult(base, head)
	wantMatch := runCompareMatch{ByID: 0, ByTitle: 3, Unmatched: 0}
	if result.Match != wantMatch {
		t.Fatalf("match = %+v, ожидали %+v", result.Match, wantMatch)
	}
	if len(result.Added)+len(result.Removed)+len(result.Changed) != 0 {
		t.Fatalf("одинаковое содержимое не должно давать diff: added=%+v removed=%+v changed=%+v",
			result.Added, result.Removed, result.Changed)
	}
	if result.Added == nil || result.Removed == nil || result.Changed == nil {
		t.Fatalf("срезы должны быть не-nil: %+v", result)
	}
	// Story поверх semantic-результата тоже пустая.
	if story, _ := buildComparisonStory(result.Added, result.Removed, result.Changed); story != comparisonStoryEmptyText {
		t.Fatalf("story = %q", story)
	}
}

func TestBuildRunCompareResultSemanticChanged(t *testing.T) {
	tests := []struct {
		name       string
		modify     func(nodes []domain.Node)
		wantFields []string
		wantHeadID string
		wantBaseID string
	}{
		{
			name: "description",
			modify: func(nodes []domain.Node) {
				nodes[1].Description = "Готовить и проверять отчёты"
			},
			wantFields: []string{"description"},
			wantHeadID: "head-function",
			wantBaseID: "base-function",
		},
		{
			name: "title case only",
			modify: func(nodes []domain.Node) {
				nodes[1].Title = "ОТЧЁТНОСТЬ."
			},
			wantFields: []string{"title"},
			wantHeadID: "head-function",
			wantBaseID: "base-function",
		},
		{
			name: "evidence quote",
			modify: func(nodes []domain.Node) {
				nodes[2].Evidence[0].Quote = "другая цитата задачи"
			},
			wantFields: []string{"evidence"},
			wantHeadID: "head-task",
			wantBaseID: "base-task",
		},
		{
			name: "evidence status",
			modify: func(nodes []domain.Node) {
				nodes[0].Evidence[0].Status = domain.EvidenceApproved
			},
			wantFields: []string{"evidence"},
			wantHeadID: "head-root",
			wantBaseID: "base-root",
		},
		{
			name: "title case and description",
			modify: func(nodes []domain.Node) {
				nodes[1].Title = "ОТЧЁТНОСТЬ"
				nodes[1].Description = "Готовить и проверять отчёты"
			},
			wantFields: []string{"title", "description"},
			wantHeadID: "head-function",
			wantBaseID: "base-function",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := semanticTree("base", "source-base")
			head := semanticTree("head", "source-head")
			test.modify(head)
			result := buildRunCompareResult(base, head)
			if result.Match != (runCompareMatch{ByID: 0, ByTitle: 3, Unmatched: 0}) {
				t.Fatalf("match = %+v", result.Match)
			}
			if len(result.Added) != 0 || len(result.Removed) != 0 {
				t.Fatalf("added/removed при semantic-совпадении: %+v / %+v", result.Added, result.Removed)
			}
			if len(result.Changed) != 1 {
				t.Fatalf("ожидали ровно один changed, получили %+v", result.Changed)
			}
			change := result.Changed[0]
			// Для semantic-пары id изменённого узла — id head-узла, before.id —
			// id узла базового прогона.
			if change.ID != test.wantHeadID || change.Before.ID != test.wantBaseID || change.After.ID != test.wantHeadID {
				t.Fatalf("id/before/after: %+v", change)
			}
			if !reflect.DeepEqual(change.Fields, test.wantFields) {
				t.Fatalf("fields = %v, ожидали %v", change.Fields, test.wantFields)
			}
			wantHeader := "Узлов добавлено: 0, удалено: 0, изменено: 1."
			story, _ := buildComparisonStory(result.Added, result.Removed, result.Changed)
			if !strings.HasPrefix(story, wantHeader) {
				t.Fatalf("story = %q", story)
			}
		})
	}
}

// TestBuildRunCompareResultRenamedNodeIsAddedRemoved: полная смена названия
// меняет semantic-ключ, поэтому узел (и его потомки, чей путь предков изменился)
// честно попадает в added/removed, а не в ложный changed.
func TestBuildRunCompareResultRenamedNodeIsAddedRemoved(t *testing.T) {
	base := semanticTree("base", "source-base")
	head := semanticTree("head", "source-head")
	head[1].Title = "Планирование и отчётность"
	result := buildRunCompareResult(base, head)
	if len(result.Changed) != 0 {
		t.Fatalf("смена названия не должна давать changed: %+v", result.Changed)
	}
	if len(result.Added) != 2 || len(result.Removed) != 2 {
		t.Fatalf("added/removed = %d/%d, ожидали 2/2", len(result.Added), len(result.Removed))
	}
	if result.Match.ByTitle != 1 { // корень совпал
		t.Fatalf("by_title = %d", result.Match.ByTitle)
	}
}

func TestBuildRunCompareResultSemanticAmbiguous(t *testing.T) {
	tests := []struct {
		name       string
		base       []domain.Node
		head       []domain.Node
		wantAdded  int
		wantRemove int
	}{
		{
			name: "two base one head",
			base: []domain.Node{
				matchTestNode("base-1", domain.NodeRole, "Аналитик", nil, "первый"),
				matchTestNode("base-2", domain.NodeRole, "Аналитик", nil, "второй"),
			},
			head:       []domain.Node{matchTestNode("head-1", domain.NodeRole, "Аналитик", nil, "третий")},
			wantAdded:  1,
			wantRemove: 2,
		},
		{
			name:       "one base two head",
			base:       []domain.Node{matchTestNode("base-1", domain.NodeRole, "Аналитик", nil, "")},
			head:       []domain.Node{matchTestNode("head-1", domain.NodeRole, "Аналитик", nil, "первый"), matchTestNode("head-2", domain.NodeRole, "Аналитик", nil, "второй")},
			wantAdded:  2,
			wantRemove: 1,
		},
		{
			name: "both sides ambiguous",
			base: []domain.Node{
				matchTestNode("base-1", domain.NodeRole, "Аналитик", nil, "a"),
				matchTestNode("base-2", domain.NodeRole, "Аналитик", nil, "b"),
			},
			head: []domain.Node{
				matchTestNode("head-1", domain.NodeRole, "Аналитик", nil, "c"),
				matchTestNode("head-2", domain.NodeRole, "Аналитик", nil, "d"),
			},
			wantAdded:  2,
			wantRemove: 2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := buildRunCompareResult(test.base, test.head)
			if len(result.Added) != test.wantAdded || len(result.Removed) != test.wantRemove {
				t.Fatalf("added/removed = %d/%d, ожидали %d/%d", len(result.Added), len(result.Removed), test.wantAdded, test.wantRemove)
			}
			if len(result.Changed) != 0 {
				t.Fatalf("неоднозначность не должна давать changed: %+v", result.Changed)
			}
			if result.Match.ByTitle != 0 {
				t.Fatalf("by_title = %d, ожидали 0", result.Match.ByTitle)
			}
			if result.Match.Unmatched != test.wantAdded+test.wantRemove {
				t.Fatalf("unmatched = %d", result.Match.Unmatched)
			}
		})
	}
}

func TestBuildRunCompareResultParentPathSeparatesNodes(t *testing.T) {
	// Одинаковые type+title, но разные предки: это разные узлы, не changed.
	baseRoot := matchTestNode("base-root", domain.NodeRole, "Аналитик", nil, "")
	headRoot := matchTestNode("head-root", domain.NodeRole, "Менеджер", nil, "")
	base := []domain.Node{
		baseRoot,
		matchTestNode("base-fn", domain.NodeFunction, "Планирование", ptrTo("base-root"), ""),
	}
	head := []domain.Node{
		headRoot,
		matchTestNode("head-fn", domain.NodeFunction, "Планирование", ptrTo("head-root"), ""),
	}
	result := buildRunCompareResult(base, head)
	if len(result.Changed) != 0 {
		t.Fatalf("разные пути предков не должны давать changed: %+v", result.Changed)
	}
	if len(result.Added) != 2 || len(result.Removed) != 2 {
		t.Fatalf("added/removed = %d/%d", len(result.Added), len(result.Removed))
	}
	if result.Match.ByTitle != 0 {
		t.Fatalf("by_title = %d", result.Match.ByTitle)
	}
}

func TestBuildRunCompareResultMatchesByIDFirst(t *testing.T) {
	// Узлы с общими id матчатся по id и не попадают в semantic-счётчик.
	base := []domain.Node{
		matchTestNode("node-root", domain.NodeRole, "Аналитик", nil, ""),
		matchTestNode("node-fn", domain.NodeFunction, "Отчётность", ptrTo("node-root"), "Готовить отчёты"),
	}
	head := []domain.Node{
		matchTestNode("node-root", domain.NodeRole, "Аналитик", nil, ""),
		matchTestNode("node-fn", domain.NodeFunction, "Отчётность", ptrTo("node-root"), "Готовить и проверять отчёты"),
	}
	result := buildRunCompareResult(base, head)
	if result.Match != (runCompareMatch{ByID: 2, ByTitle: 0, Unmatched: 0}) {
		t.Fatalf("match = %+v", result.Match)
	}
	if len(result.Changed) != 1 || !reflect.DeepEqual(result.Changed[0].Fields, []string{"description"}) {
		t.Fatalf("changed = %+v", result.Changed)
	}
}

func TestBuildRunCompareResultDeterministic(t *testing.T) {
	base := semanticTree("base", "source-base")
	head := semanticTree("head", "source-head")
	head[1].Description = "Готовить и проверять отчёты"
	first := buildRunCompareResult(base, head)

	reversedBase := []domain.Node{base[2], base[0], base[1]}
	reversedHead := []domain.Node{head[1], head[2], head[0]}
	second := buildRunCompareResult(reversedBase, reversedHead)
	if first.Match != second.Match {
		t.Fatalf("match не детерминирован: %+v vs %+v", first.Match, second.Match)
	}
	if !reflect.DeepEqual(collectionIDs(first.Changed), collectionIDs(second.Changed)) {
		t.Fatalf("changed не детерминирован: %+v vs %+v", first.Changed, second.Changed)
	}
}

// TestBuildRunCompareResponseSemanticProfession проверяет, что hero-подобная
// пара (профессии названы по-разному, корень дерева совпадает) разрешена, а
// разные профессии и корни — нет.
func TestBuildRunCompareResponseSemanticProfession(t *testing.T) {
	heroRu := []domain.Node{matchTestNode("ru-root", domain.NodeRole, "Аналитик данных", nil, "")}
	heroEn := []domain.Node{matchTestNode("en-root", domain.NodeRole, "Аналитик данных", nil, "")}
	devops := []domain.Node{matchTestNode("ops-root", domain.NodeRole, "DevOps Engineer", nil, "")}

	result, err := buildRunCompareResponse(
		&domain.Run{ID: compareTestRunID, Profession: "Data Analyst"},
		&domain.Run{ID: compareTestAgainstID, Profession: "Аналитик данных"},
		heroEn, heroRu)
	if err != nil {
		t.Fatalf("hero-подобная пара должна сравниваться: %v", err)
	}
	if !result.SameProfession || result.Match != (runCompareMatch{ByTitle: 1}) {
		t.Fatalf("same_profession/match: %+v", result)
	}

	_, err = buildRunCompareResponse(
		&domain.Run{ID: compareTestRunID, Profession: "Data Analyst"},
		&domain.Run{ID: compareTestAgainstID, Profession: "DevOps Engineer"},
		heroEn, devops)
	if !errors.Is(err, ErrRunProfessionMismatch) {
		t.Fatalf("разные профессии и корни: ожидали ErrRunProfessionMismatch, получили %v", err)
	}
}

// TestBuildRunCompareResponseSemanticEvidenceSourceIgnored фиксирует сравнение
// evidence semantic-пары по (ref_id, quote, status): технические UUID
// источника/подтверждения у независимых прогонов разные.
func TestBuildRunCompareResponseSemanticEvidenceSourceIgnored(t *testing.T) {
	base := []domain.Node{matchTestNode("base-root", domain.NodeRole, "Аналитик данных", nil, "",
		matchTestEvidence("base-ev", "source-base", "msg-1", "цитата", domain.EvidencePending))}
	head := []domain.Node{matchTestNode("head-root", domain.NodeRole, "Аналитик данных", nil, "",
		matchTestEvidence("head-ev", "source-head", "msg-1", "цитата", domain.EvidencePending))}
	result, err := buildRunCompareResponse(
		&domain.Run{ID: compareTestRunID, Profession: "Data Analyst"},
		&domain.Run{ID: compareTestAgainstID, Profession: "Data Analyst"},
		head, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changed) != 0 || result.Summary != (snapshotDiffSummary{}) {
		t.Fatalf("evidence с тем же содержимым не должно давать changed: %+v", result.Changed)
	}
	if result.Match != (runCompareMatch{ByTitle: 1}) {
		t.Fatalf("match: %+v", result.Match)
	}

	// Разная цитата при тех же title/type → changed evidence.
	head[0].Evidence[0].Quote = "другая цитата"
	result, err = buildRunCompareResponse(
		&domain.Run{ID: compareTestRunID, Profession: "Data Analyst"},
		&domain.Run{ID: compareTestAgainstID, Profession: "Data Analyst"},
		head, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changed) != 1 || !reflect.DeepEqual(result.Changed[0].Fields, []string{"evidence"}) {
		t.Fatalf("changed = %+v", result.Changed)
	}
}

func collectionIDs(changes []snapshotNodeChange) []string {
	ids := make([]string, 0, len(changes))
	for _, change := range changes {
		ids = append(ids, change.ID+":"+strings.Join(change.Fields, ","))
	}
	return ids
}
