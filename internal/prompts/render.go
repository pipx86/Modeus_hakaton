// Package prompts loads, composes, renders and fingerprints the versioned agent
// prompts embedded in the module's prompts directory.
package prompts

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	promptassets "activity-descriptor/prompts"
)

// Supported agent kinds. The strings mirror internal/agent.Kind values; a test
// keeps the two lists in sync.
const (
	// KindInterviewer is the interviewer agent prompt.
	KindInterviewer = "interviewer"
	// KindAnalyst is the analyst agent prompt.
	KindAnalyst = "analyst"
	// KindSynthesizer is the synthesizer agent prompt.
	KindSynthesizer = "synthesizer"
)

// DefaultVersion is the prompt version used when none is requested.
const DefaultVersion = "v1"

const (
	sharedSecurity    = "_shared/security.md"
	sharedOutputRules = "_shared/output_rules.md"
)

// ErrUnknownPrompt marks an unknown agent kind or prompt version.
var ErrUnknownPrompt = errors.New("prompts: unknown prompt")

// ErrUnresolvedPlaceholder marks a template placeholder without a value.
var ErrUnresolvedPlaceholder = errors.New("prompts: unresolved placeholder")

// Document is a composed prompt set for one agent version: shared security
// rules, shared output rules, the agent system prompt and few-shot examples.
type Document struct {
	Kind     string
	Version  string
	System   string
	Examples string
	// SHA256 fingerprints the composed prompt. Persist it as prompt_hash so
	// every model call can be traced back to the exact prompt version.
	SHA256 string
}

// Kinds returns supported agent kinds in stable order.
func Kinds() []string {
	return []string{KindInterviewer, KindAnalyst, KindSynthesizer}
}

// Load composes the prompt document for kind/version from the embedded prompts.
// The empty version means DefaultVersion.
func Load(kind, version string) (Document, error) {
	return compose(kind, version, readAsset)
}

// compose builds a prompt document using read, which resolves slash-separated
// prompt paths (e.g. interviewer/v1/system.md). It is shared by the embedded
// loader and the file-based registry.
func compose(kind, version string, read func(name string) (string, error)) (Document, error) {
	if version == "" {
		version = DefaultVersion
	}
	if !validKind(kind) {
		return Document{}, fmt.Errorf("%w: kind %q", ErrUnknownPrompt, kind)
	}

	security, err := read(sharedSecurity)
	if err != nil {
		return Document{}, err
	}
	outputRules, err := read(sharedOutputRules)
	if err != nil {
		return Document{}, err
	}
	system, err := read(kind + "/" + version + "/system.md")
	if err != nil {
		return Document{}, fmt.Errorf("%w: %s/%s: %v", ErrUnknownPrompt, kind, version, err)
	}
	examples, err := read(kind + "/" + version + "/examples.md")
	if err != nil {
		return Document{}, fmt.Errorf("%w: %s/%s: %v", ErrUnknownPrompt, kind, version, err)
	}

	doc := Document{
		Kind:     kind,
		Version:  version,
		System:   strings.Join([]string{security, outputRules, system}, "\n\n---\n\n"),
		Examples: examples,
	}
	doc.SHA256 = fingerprint(doc)
	return doc, nil
}

// Render substitutes {{name}} placeholders in template with vars. It fails when
// a placeholder has no value, so a prompt cannot silently ship unrendered.
func Render(template string, vars map[string]string) (string, error) {
	var out strings.Builder
	var unresolved []string
	rest := template
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			out.WriteString(rest)
			break
		}
		end := strings.Index(rest[start:], "}}")
		if end < 0 {
			return "", fmt.Errorf("%w: unclosed placeholder", ErrUnresolvedPlaceholder)
		}
		end += start
		name := strings.TrimSpace(rest[start+2 : end])
		out.WriteString(rest[:start])
		if value, ok := vars[name]; ok {
			out.WriteString(value)
		} else {
			unresolved = append(unresolved, name)
		}
		rest = rest[end+2:]
	}
	if len(unresolved) > 0 {
		sort.Strings(unresolved)
		return "", fmt.Errorf("%w: %s", ErrUnresolvedPlaceholder, strings.Join(unresolved, ", "))
	}
	return out.String(), nil
}

// RenderDocument renders both the system prompt and the examples of doc and
// recomputes the fingerprint.
func RenderDocument(doc Document, vars map[string]string) (Document, error) {
	system, err := Render(doc.System, vars)
	if err != nil {
		return Document{}, fmt.Errorf("prompts: render system: %w", err)
	}
	examples, err := Render(doc.Examples, vars)
	if err != nil {
		return Document{}, fmt.Errorf("prompts: render examples: %w", err)
	}
	rendered := Document{
		Kind: doc.Kind, Version: doc.Version,
		System: system, Examples: examples,
	}
	rendered.SHA256 = fingerprint(rendered)
	return rendered, nil
}

// Versions returns the sorted prompt versions available for kind.
func Versions(kind string) ([]string, error) {
	if !validKind(kind) {
		return nil, fmt.Errorf("%w: kind %q", ErrUnknownPrompt, kind)
	}
	entries, err := promptassets.FS.ReadDir(kind)
	if err != nil {
		return nil, fmt.Errorf("%w: read %s prompts: %v", ErrUnknownPrompt, kind, err)
	}
	versions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		versions = append(versions, entry.Name())
	}
	sort.Strings(versions)
	if len(versions) == 0 {
		return nil, fmt.Errorf("%w: no versions for %s", ErrUnknownPrompt, kind)
	}
	return versions, nil
}

func readAsset(name string) (string, error) {
	data, err := promptassets.FS.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("prompts: read %s: %w", name, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", fmt.Errorf("prompts: %s is empty", name)
	}
	return strings.TrimSpace(string(data)), nil
}

func validKind(kind string) bool {
	switch kind {
	case KindInterviewer, KindAnalyst, KindSynthesizer:
		return true
	default:
		return false
	}
}

func fingerprint(doc Document) string {
	sum := sha256.Sum256([]byte(doc.Kind + "\x00" + doc.Version + "\x00" + doc.System + "\x00" + doc.Examples))
	return hex.EncodeToString(sum[:])
}
