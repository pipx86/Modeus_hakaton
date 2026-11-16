package prompts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewRegistryLoadsEmbeddedPrompts(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if got, want := registry.Len(), len(Kinds()); got != want {
		t.Fatalf("registry len = %d, want %d", got, want)
	}
	for _, kind := range Kinds() {
		doc, err := registry.Resolve(kind, "")
		if err != nil {
			t.Fatalf("Resolve(%q): %v", kind, err)
		}
		if doc.Version != DefaultVersion || doc.SHA256 == "" {
			t.Fatalf("resolved doc = %+v", doc)
		}
		versions := registry.Versions(kind)
		if len(versions) == 0 || versions[0] != DefaultVersion {
			t.Fatalf("versions(%q) = %v", kind, versions)
		}
	}
	if _, err := registry.Resolve(KindInterviewer, "v99"); !errors.Is(err, ErrUnknownPrompt) {
		t.Fatalf("unknown version error = %v, want ErrUnknownPrompt", err)
	}
	kinds := registry.Kinds()
	if len(kinds) != len(Kinds()) {
		t.Fatalf("registry kinds = %v", kinds)
	}
}

func TestRegistryRegister(t *testing.T) {
	registry := &Registry{}
	doc := Document{
		Kind: KindInterviewer, Version: "v2",
		System: "тестовый системный промпт", Examples: "пример",
	}
	if err := registry.Register(doc); err != nil {
		t.Fatalf("Register: %v", err)
	}
	resolved, err := registry.Resolve(KindInterviewer, "v2")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.SHA256 == "" {
		t.Fatal("registered prompt has no fingerprint")
	}
	if err := registry.Register(doc); !errors.Is(err, ErrPromptRegistered) {
		t.Fatalf("duplicate Register error = %v, want ErrPromptRegistered", err)
	}
	if err := registry.Register(Document{Kind: "nope", Version: "v1", System: "x"}); !errors.Is(err, ErrUnknownPrompt) {
		t.Fatalf("unknown kind Register error = %v, want ErrUnknownPrompt", err)
	}
	if err := registry.Register(Document{Kind: KindAnalyst, System: "x"}); !errors.Is(err, ErrUnknownPrompt) {
		t.Fatalf("empty version Register error = %v, want ErrUnknownPrompt", err)
	}
	if err := registry.Register(Document{Kind: KindAnalyst, Version: "v1"}); !errors.Is(err, ErrUnknownPrompt) {
		t.Fatalf("empty system Register error = %v, want ErrUnknownPrompt", err)
	}
}

func writePromptFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoadRegistryDir(t *testing.T) {
	dir := t.TempDir()
	writePromptFile(t, dir, "_shared/security.md", "Правила безопасности из файла")
	writePromptFile(t, dir, "_shared/output_rules.md", "Правила вывода из файла")
	writePromptFile(t, dir, "interviewer/v1/system.md", "КАСТОМНЫЙ промпт интервьюера")
	writePromptFile(t, dir, "interviewer/v1/examples.md", "Пример из файла")

	registry, err := LoadRegistryDir(dir)
	if err != nil {
		t.Fatalf("LoadRegistryDir: %v", err)
	}
	doc, err := registry.Resolve(KindInterviewer, "v1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(doc.System, "КАСТОМНЫЙ") || !strings.Contains(doc.System, "Правила безопасности из файла") {
		t.Fatalf("file-based prompt was not composed: %q", doc.System)
	}
	if got := registry.Kinds(); len(got) != 1 || got[0] != KindInterviewer {
		t.Fatalf("registry kinds = %v", got)
	}
	if got := registry.Versions(KindInterviewer); len(got) != 1 || got[0] != "v1" {
		t.Fatalf("registry versions = %v", got)
	}
}

func TestLoadRegistryDirErrors(t *testing.T) {
	if _, err := LoadRegistryDir(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing directory accepted, want error")
	}
	dir := t.TempDir()
	writePromptFile(t, dir, "interviewer/v1/system.md", "промпт без shared-файлов")
	writePromptFile(t, dir, "interviewer/v1/examples.md", "пример")
	if _, err := LoadRegistryDir(dir); err == nil {
		t.Fatal("directory without shared rules accepted, want error")
	}
	if _, err := LoadRegistryDir(filepath.Join(dir, "interviewer", "v1", "system.md")); err == nil {
		t.Fatal("file path accepted as prompt directory, want error")
	}
}
