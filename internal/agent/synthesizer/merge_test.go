package synthesizer

import (
	"encoding/json"
	"testing"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
)

// testEvidence builds one valid evidence item so the merge contract
// (source + quote + ref) is satisfied.
func testEvidence(id, sourceID, refID, quote string) domain.Evidence {
	return domain.Evidence{ID: id, SourceID: sourceID, RefID: refID, Quote: quote}
}

func conflictForField(t *testing.T, conflicts []domain.Conflict, nodeID, field string) domain.Conflict {
	t.Helper()
	conflict, found := findConflict(conflicts, nodeID, field)
	if !found {
		t.Fatalf("no %s conflict for node %q in %+v", field, nodeID, conflicts)
	}
	return conflict
}

func findConflict(conflicts []domain.Conflict, nodeID, field string) (domain.Conflict, bool) {
	for _, conflict := range conflicts {
		if conflict.NodeID == nodeID && conflict.Field == field {
			return conflict, true
		}
	}
	return domain.Conflict{}, false
}

func valueBySource(values []domain.ConflictValue, sourceID string) (domain.ConflictValue, bool) {
	for _, value := range values {
		if value.SourceID == sourceID {
			return value, true
		}
	}
	return domain.ConflictValue{}, false
}

// TestMergeKeepsBothSameTypeSourceValuesAndTheirSources is the карточка 9
// regression test: two standards disagree on one task, so both positions must be
// reported and each one must keep the material it came from. Before per-node
// source resolution every value of a slot was attributed to the first source of
// that type, which made the losing position impossible to trace.
func TestMergeKeepsBothSameTypeSourceValuesAndTheirSources(t *testing.T) {
	const (
		standardA = "std-a"
		standardB = "std-b"
		taskID    = "tax-task-sql"
	)
	in := agent.SynthesizerInput{
		Profession: "Аналитик данных",
		Sources: []domain.Source{
			{ID: standardA, Type: domain.SourceStandard, Title: "Профстандарт A"},
			{ID: standardB, Type: domain.SourceStandard, Title: "Профстандарт B"},
		},
		Standard: []domain.Node{
			{
				ID: taskID, Type: domain.NodeTask, Title: "Готовить SQL-выгрузки",
				Evidence: []domain.Evidence{testEvidence("ev-a", standardA, "req-1", "Подготовка SQL-выгрузок")},
			},
			{
				ID: taskID, Type: domain.NodeTask, Title: "Писать SQL-запросы",
				Evidence: []domain.Evidence{testEvidence("ev-b", standardB, "req-2", "Написание SQL-запросов")},
			},
		},
	}

	out, err := Merge(in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(out.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want one title conflict", out.Conflicts)
	}
	conflict := conflictForField(t, out.Conflicts, taskID, "title")
	if len(conflict.Values) != 2 {
		t.Fatalf("conflict values = %+v, want both positions", conflict.Values)
	}
	if conflict.Winner.SourceType != domain.SourceStandard {
		t.Fatalf("winner = %+v, want standard", conflict.Winner)
	}

	// Each position keeps its own material: the losing value is not rewritten to
	// the winning source and both sources stay visible.
	first, ok := valueBySource(conflict.Values, standardA)
	if !ok || first.Value != "Готовить SQL-выгрузки" {
		t.Fatalf("value of %q = %+v, %v", standardA, first, ok)
	}
	second, ok := valueBySource(conflict.Values, standardB)
	if !ok || second.Value != "Писать SQL-запросы" {
		t.Fatalf("losing value of %q = %+v, %v", standardB, second, ok)
	}
	if conflict.Winner.Value != first.Value {
		t.Fatalf("winner value = %q, want the first highest-priority value %q", conflict.Winner.Value, first.Value)
	}

	node := nodeIndex(out.Tree)[taskID]
	if node.Title != conflict.Winner.Value {
		t.Fatalf("merged title = %q, want the winning value %q", node.Title, conflict.Winner.Value)
	}
	if len(node.Evidence) != 2 {
		t.Fatalf("merged evidence = %+v, want the quotes of both sources", node.Evidence)
	}
	// The reported conflict reaches the agent memory with both values.
	if len(out.Messages) != 1 {
		t.Fatalf("messages = %+v, want one conflict message", out.Messages)
	}
	var payload domain.Conflict
	if err := json.Unmarshal(out.Messages[0].Payload, &payload); err != nil {
		t.Fatalf("conflict payload: %v", err)
	}
	if len(payload.Values) != 2 {
		t.Fatalf("message payload values = %+v, want both positions", payload.Values)
	}
}

// TestMergePriorityDecidesWinnerButKeepsLosingValue pins the карточка 9 rule that
// a higher source priority only decides the merged field: the interview value
// wins the node title, while the vacancy position stays in the conflict with its
// own source and priority.
func TestMergePriorityDecidesWinnerButKeepsLosingValue(t *testing.T) {
	const (
		interviewSource = "src-interview-1"
		vacancySource   = "src-vacancy-1"
		functionID      = "tax-fn-reporting"
	)
	in := agent.SynthesizerInput{
		Profession: "Аналитик данных",
		Sources: []domain.Source{
			{ID: interviewSource, Type: domain.SourceInterview, Title: "Интервью"},
			{ID: vacancySource, Type: domain.SourceVacancy, Title: "Вакансия"},
		},
		Interview: []domain.Node{{
			ID: functionID, Type: domain.NodeFunction, Title: "Отчётность и визуализация",
			Evidence: []domain.Evidence{testEvidence("ev-i", interviewSource, "msg-1", "отчётность и визуализация")},
		}},
		Vacancy: []domain.Node{{
			ID: functionID, Type: domain.NodeFunction, Title: "Отчётность",
			Evidence: []domain.Evidence{testEvidence("ev-v", vacancySource, "line-1", "отчётность")},
		}},
	}

	out, err := Merge(in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	conflict := conflictForField(t, out.Conflicts, functionID, "title")
	if len(conflict.Values) != 2 {
		t.Fatalf("conflict values = %+v, want interview and vacancy", conflict.Values)
	}
	if conflict.Winner.SourceID != interviewSource || conflict.Winner.Priority != domain.PriorityInterview {
		t.Fatalf("winner = %+v, want interview with priority %d", conflict.Winner, domain.PriorityInterview)
	}
	loser, ok := valueBySource(conflict.Values, vacancySource)
	if !ok {
		t.Fatalf("losing vacancy value missing from %+v", conflict.Values)
	}
	if loser.Value != "Отчётность" || loser.Priority != domain.PriorityVacancy {
		t.Fatalf("losing value = %+v, want the vacancy wording and priority", loser)
	}
	if conflict.Reason == "" {
		t.Fatal("conflict has no reason explaining the resolution")
	}

	node := nodeIndex(out.Tree)[functionID]
	if node.Title != "Отчётность и визуализация" {
		t.Fatalf("merged title = %q, want the interview value", node.Title)
	}
	if len(node.Evidence) != 2 {
		t.Fatalf("merged evidence = %+v, want both confirming quotes", node.Evidence)
	}

	// The empty description of both sources must not fabricate a conflict.
	if _, found := findConflict(out.Conflicts, functionID, "description"); found {
		t.Fatal("unexpected description conflict without competing descriptions")
	}
}
