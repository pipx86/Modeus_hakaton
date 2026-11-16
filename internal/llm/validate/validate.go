// Package validate checks LLM JSON output against the project contract.
package validate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// JSON reports whether data is valid JSON.
func JSON(data []byte) error {
	if !json.Valid(data) {
		return fmt.Errorf("validate: invalid JSON")
	}
	return nil
}

// Schema validates data against a JSON schema document.
func Schema(data, schema []byte) error {
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("validate: invalid JSON: %w", err)
	}

	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return fmt.Errorf("validate: schema: %w", err)
	}

	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", doc); err != nil {
		return fmt.Errorf("validate: add schema: %w", err)
	}
	compiled, err := compiler.Compile("schema.json")
	if err != nil {
		return fmt.Errorf("validate: compile schema: %w", err)
	}
	if err := compiled.Validate(document); err != nil {
		return fmt.Errorf("validate: schema mismatch: %w", err)
	}
	return nil
}

// ContainsQuote reports whether quote appears in source after whitespace
// normalization. Empty quotes never match.
func ContainsQuote(source, quote string) bool {
	normalizedQuote := normalize(quote)
	if normalizedQuote == "" {
		return false
	}
	return strings.Contains(normalize(source), normalizedQuote)
}

func normalize(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
