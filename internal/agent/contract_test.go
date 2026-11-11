package agent

import (
	"encoding/json"
	"testing"

	"activity-descriptor/internal/domain"
)

func ptr(s string) *string { return &s }

func evidence(id string) domain.Evidence {
	return domain.Evidence{ID: id, SourceID: "src-interview", RefID: "msg-1", Quote: "каждый день пишу SQL-запросы"}
}

func validNode(id string, level domain.NodeType) domain.Node {
	return domain.Node{
		ID:       id,
		Type:     level,
		Title:    "Задача уровня " + string(level),
		ParentID: ptr("parent-" + id),
		Evidence: []domain.Evidence{evidence("ev-" + id)},
	}
}

func validInterviewer() InterviewerOutput {
	return InterviewerOutput{
		Question: domain.Question{
			ID: "q-1", Level: domain.QuestionTask,
			Text: "Какие задачи вы выполняете каждый день?",
		},
		Rationale:       "нужно спуститься с уровня функции к задачам",
		IsClarification: false,
	}
}

func validAnalyst() AnalystOutput {
	entity := ExtractedEntity{Node: validNode("tax-task-sql", domain.NodeTask), Confidence: 0.9}
	return AnalystOutput{Entities: []ExtractedEntity{entity}}
}

func validSynthesizer() SynthesizerOutput {
	winner := domain.ConflictValue{
		SourceID: "src-interview", SourceType: domain.SourceInterview,
		Value: "Писать SQL-запросы", Priority: domain.PriorityInterview,
	}
	loser := domain.ConflictValue{
		SourceID: "src-vacancy", SourceType: domain.SourceVacancy,
		Value: "Готовить выгрузки", Priority: domain.PriorityVacancy,
	}
	conflict := domain.Conflict{
		ID: "c-1", NodeID: "tax-task-sql", Field: "title",
		Values: []domain.ConflictValue{winner, loser},
		Winner: winner,
		Reason: "interview beats vacancy",
	}
	payload, err := json.Marshal(conflict)
	if err != nil {
		panic(err)
	}
	root := validNode("node-root", domain.NodeRole)
	root.ParentID = nil
	return SynthesizerOutput{
		Tree:      []domain.Node{root, validNode("tax-task-sql", domain.NodeTask)},
		Conflicts: []domain.Conflict{conflict},
		Messages: []AgentMessage{{
			ID: "msg-1", Kind: "conflict",
			Text:    "источники расходятся в названии",
			Payload: payload,
		}},
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func TestSchemaAvailableForEveryKind(t *testing.T) {
	for _, kind := range Kinds() {
		schema, err := Schema(kind)
		if err != nil {
			t.Fatalf("Schema(%q): %v", kind, err)
		}
		if len(schema) == 0 {
			t.Fatalf("Schema(%q) is empty", kind)
		}
		var raw map[string]any
		if err := json.Unmarshal(schema, &raw); err != nil {
			t.Fatalf("schema %q is not JSON: %v", kind, err)
		}
		if raw["$schema"] == nil {
			t.Fatalf("schema %q has no $schema", kind)
		}
	}
	if _, err := Schema(Kind("unknown")); err == nil {
		t.Fatal("unknown kind accepted by Schema, want error")
	}
}

func TestValidateOutputInterviewer(t *testing.T) {
	if err := ValidateOutput(KindInterviewer, mustJSON(t, validInterviewer())); err != nil {
		t.Fatalf("valid interviewer output rejected: %v", err)
	}

	missingRationale := validInterviewer()
	missingRationale.Rationale = ""
	// rationale is required but may be an empty string in JSON; remove it fully.
	invalid := map[string]any{"question": missingRationale.Question, "is_clarification": false}
	if err := ValidateOutput(KindInterviewer, mustJSON(t, invalid)); err == nil {
		t.Fatal("output without rationale passed schema, want error")
	}

	badLevel := validInterviewer()
	badLevel.Question.Level = domain.QuestionLevel("goal")
	if err := ValidateOutput(KindInterviewer, mustJSON(t, badLevel)); err == nil {
		t.Fatal("invalid question level passed schema, want error")
	}
}

func TestValidateOutputAnalyst(t *testing.T) {
	if err := ValidateOutput(KindAnalyst, mustJSON(t, validAnalyst())); err != nil {
		t.Fatalf("valid analyst output rejected: %v", err)
	}

	noRef := validAnalyst()
	noRef.Entities[0].Evidence[0].RefID = ""
	if err := ValidateOutput(KindAnalyst, mustJSON(t, noRef)); err == nil {
		t.Fatal("entity without ref_id passed schema, want error")
	}

	noQuote := validAnalyst()
	noQuote.Entities[0].Evidence[0].Quote = ""
	if err := ValidateOutput(KindAnalyst, mustJSON(t, noQuote)); err == nil {
		t.Fatal("entity without quote passed schema, want error")
	}

	noEvidence := validAnalyst()
	noEvidence.Entities[0].Evidence = nil
	if err := ValidateOutput(KindAnalyst, mustJSON(t, noEvidence)); err == nil {
		t.Fatal("entity without evidence passed schema, want error")
	}

	empty := AnalystOutput{Entities: []ExtractedEntity{}}
	if err := ValidateOutput(KindAnalyst, mustJSON(t, empty)); err == nil {
		t.Fatal("empty entity list passed schema, want error")
	}
}

func TestValidateOutputSynthesizer(t *testing.T) {
	if err := ValidateOutput(KindSynthesizer, mustJSON(t, validSynthesizer())); err != nil {
		t.Fatalf("valid synthesizer output rejected: %v", err)
	}

	noEvidence := validSynthesizer()
	noEvidence.Tree[1].Evidence = nil
	if err := ValidateOutput(KindSynthesizer, mustJSON(t, noEvidence)); err == nil {
		t.Fatal("tree node without evidence passed schema, want error")
	}

	singleValue := validSynthesizer()
	singleValue.Conflicts[0].Values = singleValue.Conflicts[0].Values[:1]
	if err := ValidateOutput(KindSynthesizer, mustJSON(t, singleValue)); err == nil {
		t.Fatal("conflict with a single value passed schema, want error")
	}
}

func TestDecodeOutput(t *testing.T) {
	data := mustJSON(t, validInterviewer())
	var got InterviewerOutput
	if err := DecodeOutput(KindInterviewer, data, &got); err != nil {
		t.Fatalf("DecodeOutput: %v", err)
	}
	if got.Question.ID != "q-1" {
		t.Fatalf("decoded question id = %q, want q-1", got.Question.ID)
	}
	if err := DecodeOutput(KindInterviewer, data, nil); err == nil {
		t.Fatal("nil decode target accepted, want error")
	}
	if err := DecodeOutput(Kind("nope"), data, &got); err == nil {
		t.Fatal("unknown kind accepted by DecodeOutput, want error")
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	env := Envelope{
		Contract:      ContractID,
		Kind:          KindInterviewer,
		PromptVersion: "v1",
		TraceID:       "run-1",
		Payload:       mustJSON(t, validInterviewer()),
	}
	if env.Contract != "agent.io/v1" {
		t.Fatalf("ContractID = %q", env.Contract)
	}
	if err := ValidateOutput(env.Kind, env.Payload); err != nil {
		t.Fatalf("envelope payload rejected: %v", err)
	}
}

func TestKindsAreStable(t *testing.T) {
	if ContractVersion == "" {
		t.Fatal("ContractVersion is empty")
	}
	kinds := SortedKinds()
	if len(kinds) != len(Kinds()) {
		t.Fatalf("SortedKinds lost kinds: %v", kinds)
	}
	for i := 1; i < len(kinds); i++ {
		if kinds[i-1] >= kinds[i] {
			t.Fatalf("kinds not sorted: %v", kinds)
		}
	}
}
