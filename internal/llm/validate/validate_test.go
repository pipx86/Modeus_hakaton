package validate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJSON(t *testing.T) {
	if err := JSON([]byte(`{"a":1}`)); err != nil {
		t.Fatalf("valid JSON rejected: %v", err)
	}
	if err := JSON([]byte(`{"a":`)); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

func TestSchemaNode(t *testing.T) {
	schema, err := os.ReadFile(filepath.Join("..", "schema", "node.schema.json"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}

	valid := []byte(`{
		"id": "n-1",
		"type": "action",
		"title": "Написать SQL-запрос",
		"evidence": [{"id": "e-1", "source_id": "s-1", "ref_id": "msg-1", "quote": "каждый день пишу запросы"}]
	}`)
	if err := Schema(valid, schema); err != nil {
		t.Fatalf("valid node rejected: %v", err)
	}

	invalid := []byte(`{"id": "n-1", "type": "action", "title": "Без evidence"}`)
	if err := Schema(invalid, schema); err == nil {
		t.Fatal("node without evidence accepted")
	}
}

func TestContainsQuote(t *testing.T) {
	source := "Каждый день я пишу\nSQL-запросы и строю отчёты."
	if !ContainsQuote(source, "пишу SQL-запросы") {
		t.Fatal("existing quote not found")
	}
	if ContainsQuote(source, "пишу Python-скрипты") {
		t.Fatal("missing quote reported as found")
	}
	if ContainsQuote(source, "   ") {
		t.Fatal("blank quote must not match")
	}
}
