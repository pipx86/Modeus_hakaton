package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func compile(t *testing.T, file string) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "llm", "schema", file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(file, doc); err != nil {
		t.Fatalf("add %s: %v", file, err)
	}
	sch, err := c.Compile(file)
	if err != nil {
		t.Fatalf("compile %s: %v", file, err)
	}
	return sch
}

func roundTrip(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func validNode() Node {
	pid := "n-root"
	return Node{
		ID: "n-1", Type: NodeAction, Title: "Написать SQL-запрос",
		Description: "выгрузка отчётности", ParentID: &pid,
		Evidence: []Evidence{{ID: "e-1", SourceID: "s-1", RefID: "msg-1", Quote: "каждый день пишу запросы"}},
	}
}

func TestNodeSchemaValid(t *testing.T) {
	if err := compile(t, "node.schema.json").Validate(roundTrip(t, validNode())); err != nil {
		t.Fatalf("valid node rejected by schema: %v", err)
	}
}

func TestNodeSchemaMissingEvidence(t *testing.T) {
	n := validNode()
	n.Evidence = nil
	if err := compile(t, "node.schema.json").Validate(roundTrip(t, n)); err == nil {
		t.Fatal("node without evidence passed schema, want error")
	}
}

func TestNodeDomainEvidenceRequired(t *testing.T) {
	n := validNode()
	n.Evidence = nil
	if !errors.Is(n.Validate(), ErrEvidenceRequired) {
		t.Fatalf("want EVIDENCE_REQUIRED, got %v", n.Validate())
	}
}

func TestNodeDomainOK(t *testing.T) {
	if err := validNode().Validate(); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

func TestEvidenceSchemaValid(t *testing.T) {
	e := Evidence{ID: "e-1", SourceID: "s-1", Quote: "q"}
	if err := compile(t, "evidence.schema.json").Validate(roundTrip(t, e)); err != nil {
		t.Fatalf("evidence rejected: %v", err)
	}
}

func TestSourceSchemaValid(t *testing.T) {
	now := time.Now().UTC()
	s := Source{ID: "s-1", Type: SourceVacancy, Title: "Data Analyst", Content: "...", CreatedAt: &now}
	if err := compile(t, "source.schema.json").Validate(roundTrip(t, s)); err != nil {
		t.Fatalf("source rejected: %v", err)
	}
}

func TestRunSchemaValid(t *testing.T) {
	root := "n-root"
	r := Run{ID: "r-1", Status: RunRunning, Profession: "Аналитик данных", RootNodeID: &root}
	if err := compile(t, "run.schema.json").Validate(roundTrip(t, r)); err != nil {
		t.Fatalf("run rejected: %v", err)
	}
}
