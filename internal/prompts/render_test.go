package prompts

import (
	"errors"
	"strings"
	"testing"

	"activity-descriptor/internal/agent"
)

func TestLoadEveryAgentVersion(t *testing.T) {
	for _, kind := range Kinds() {
		versions, err := Versions(kind)
		if err != nil {
			t.Fatalf("Versions(%q): %v", kind, err)
		}
		foundDefault := false
		for _, version := range versions {
			doc, err := Load(kind, version)
			if err != nil {
				t.Fatalf("Load(%q, %q): %v", kind, version, err)
			}
			if doc.Kind != kind || doc.Version != version {
				t.Fatalf("doc identity = %s/%s, want %s/%s", doc.Kind, doc.Version, kind, version)
			}
			if !strings.Contains(doc.System, "ДАННЫЕ") {
				t.Fatalf("%s/%s system prompt misses shared security rules", kind, version)
			}
			if !strings.Contains(doc.System, "валидный JSON") {
				t.Fatalf("%s/%s system prompt misses shared output rules", kind, version)
			}
			if strings.TrimSpace(doc.Examples) == "" {
				t.Fatalf("%s/%s examples are empty", kind, version)
			}
			if len(doc.SHA256) != 64 {
				t.Fatalf("%s/%s hash length = %d, want 64", kind, version, len(doc.SHA256))
			}
			if version == DefaultVersion {
				foundDefault = true
			}
		}
		if !foundDefault {
			t.Fatalf("%q has no %s version: %v", kind, DefaultVersion, versions)
		}
	}
}

func TestLoadDeterministic(t *testing.T) {
	first, err := Load(KindInterviewer, DefaultVersion)
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	second, err := Load(KindInterviewer, DefaultVersion)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if first.SHA256 != second.SHA256 {
		t.Fatalf("prompt hash is not reproducible: %s vs %s", first.SHA256, second.SHA256)
	}
	if first.System != second.System {
		t.Fatal("composed system prompts differ between loads")
	}
}

func TestLoadEmptyVersionUsesDefault(t *testing.T) {
	doc, err := Load(KindAnalyst, "")
	if err != nil {
		t.Fatalf("Load with empty version: %v", err)
	}
	if doc.Version != DefaultVersion {
		t.Fatalf("version = %q, want %q", doc.Version, DefaultVersion)
	}
}

func TestLoadUnknownPrompt(t *testing.T) {
	if _, err := Load("nope", DefaultVersion); !errors.Is(err, ErrUnknownPrompt) {
		t.Fatalf("unknown kind error = %v, want ErrUnknownPrompt", err)
	}
	if _, err := Load(KindInterviewer, "v99"); !errors.Is(err, ErrUnknownPrompt) {
		t.Fatalf("unknown version error = %v, want ErrUnknownPrompt", err)
	}
}

func TestVersionsUnknownKind(t *testing.T) {
	if _, err := Versions("nope"); !errors.Is(err, ErrUnknownPrompt) {
		t.Fatalf("Versions unknown kind error = %v, want ErrUnknownPrompt", err)
	}
}

func TestRender(t *testing.T) {
	got, err := Render("Роль: {{role}}, профессия: {{profession}}.", map[string]string{
		"role": "аналитик", "profession": "Аналитик данных",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "Роль: аналитик, профессия: Аналитик данных."
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}

	if _, err := Render("{{role}} / {{missing}}", map[string]string{"role": "x"}); !errors.Is(err, ErrUnresolvedPlaceholder) {
		t.Fatalf("unresolved placeholder error = %v, want ErrUnresolvedPlaceholder", err)
	}
	if _, err := Render("{{role", nil); !errors.Is(err, ErrUnresolvedPlaceholder) {
		t.Fatalf("unclosed placeholder error = %v, want ErrUnresolvedPlaceholder", err)
	}
	if got, err := Render("без подстановок", nil); err != nil || got != "без подстановок" {
		t.Fatalf("Render without placeholders = %q, %v", got, err)
	}
}

func TestRenderDocumentRecomputesHash(t *testing.T) {
	doc, err := Load(KindInterviewer, DefaultVersion)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rendered, err := RenderDocument(doc, map[string]string{})
	if err != nil {
		t.Fatalf("RenderDocument: %v", err)
	}
	if rendered.SHA256 != doc.SHA256 {
		t.Fatal("rendering with no substitutions must keep the hash stable")
	}

	templated := Document{
		Kind: "interviewer", Version: "v1",
		System:   "Профессия респондента: {{profession}}.",
		Examples: "Пример для {{profession}}.",
	}
	templated.SHA256 = fingerprint(templated)
	filled, err := RenderDocument(templated, map[string]string{"profession": "Аналитик данных"})
	if err != nil {
		t.Fatalf("RenderDocument with vars: %v", err)
	}
	if !strings.Contains(filled.System, "Аналитик данных") {
		t.Fatalf("rendered system = %q, want substituted profession", filled.System)
	}
	if filled.SHA256 == templated.SHA256 {
		t.Fatal("rendering with substitutions must change the hash")
	}
	if _, err := RenderDocument(templated, nil); !errors.Is(err, ErrUnresolvedPlaceholder) {
		t.Fatalf("RenderDocument without vars error = %v, want ErrUnresolvedPlaceholder", err)
	}
}

func TestKindsMatchAgentContract(t *testing.T) {
	want := make(map[string]bool)
	for _, kind := range agent.Kinds() {
		want[string(kind)] = true
	}
	for _, kind := range Kinds() {
		if !want[kind] {
			t.Fatalf("prompt kind %q is missing from agent.Kinds()", kind)
		}
		delete(want, kind)
	}
	for kind := range want {
		t.Fatalf("agent kind %q has no prompt kind", kind)
	}
}
