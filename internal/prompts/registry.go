package prompts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ErrPromptRegistered marks a duplicate prompt registration.
var ErrPromptRegistered = errors.New("prompts: already registered")

// Registry is an in-memory prompt registry. It can be built from the embedded
// prompts or from a prompt directory on disk, which is the file-based MVP for
// experimenting with prompts without rebuilding the binary.
type Registry struct {
	mu   sync.RWMutex
	docs map[string]Document
}

// NewRegistry loads every embedded prompt version into a registry.
func NewRegistry() (*Registry, error) {
	registry := &Registry{docs: make(map[string]Document)}
	for _, kind := range Kinds() {
		versions, err := Versions(kind)
		if err != nil {
			return nil, err
		}
		for _, version := range versions {
			doc, err := Load(kind, version)
			if err != nil {
				return nil, err
			}
			if err := registry.Register(doc); err != nil {
				return nil, err
			}
		}
	}
	return registry, nil
}

// LoadRegistryDir builds a registry from prompt files on disk. The layout is
// identical to the embedded prompts directory:
//
//	<dir>/_shared/security.md
//	<dir>/_shared/output_rules.md
//	<dir>/<agent>/<version>/system.md
//	<dir>/<agent>/<version>/examples.md
func LoadRegistryDir(dir string) (*Registry, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("prompts: stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("prompts: %s is not a directory", dir)
	}

	read := func(name string) (string, error) {
		path := filepath.Join(dir, filepath.FromSlash(name))
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("prompts: read %s: %w", path, err)
		}
		trimmed := strings.TrimSpace(string(data))
		if trimmed == "" {
			return "", fmt.Errorf("prompts: %s is empty", path)
		}
		return trimmed, nil
	}

	registry := &Registry{docs: make(map[string]Document)}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("prompts: read %s: %w", dir, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !validKind(entry.Name()) {
			continue
		}
		kind := entry.Name()
		versionEntries, err := os.ReadDir(filepath.Join(dir, kind))
		if err != nil {
			return nil, fmt.Errorf("prompts: read %s: %w", kind, err)
		}
		for _, versionEntry := range versionEntries {
			if !versionEntry.IsDir() {
				continue
			}
			doc, err := compose(kind, versionEntry.Name(), read)
			if err != nil {
				return nil, err
			}
			if err := registry.Register(doc); err != nil {
				return nil, err
			}
		}
	}
	if registry.Len() == 0 {
		return nil, fmt.Errorf("%w: no prompts under %s", ErrUnknownPrompt, dir)
	}
	return registry, nil
}

// Register adds a prompt document. The kind and version must be set; the
// fingerprint is computed when missing.
func (r *Registry) Register(doc Document) error {
	if !validKind(doc.Kind) {
		return fmt.Errorf("%w: kind %q", ErrUnknownPrompt, doc.Kind)
	}
	if strings.TrimSpace(doc.Version) == "" {
		return fmt.Errorf("%w: empty version for %s", ErrUnknownPrompt, doc.Kind)
	}
	if strings.TrimSpace(doc.System) == "" {
		return fmt.Errorf("%w: empty system prompt for %s/%s", ErrUnknownPrompt, doc.Kind, doc.Version)
	}
	if doc.SHA256 == "" {
		doc.SHA256 = fingerprint(doc)
	}
	key := registryKey(doc.Kind, doc.Version)

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.docs == nil {
		r.docs = make(map[string]Document)
	}
	if _, duplicate := r.docs[key]; duplicate {
		return fmt.Errorf("%w: %s", ErrPromptRegistered, key)
	}
	r.docs[key] = doc
	return nil
}

// Resolve returns the prompt document for kind/version. An empty version means
// DefaultVersion.
func (r *Registry) Resolve(kind, version string) (Document, error) {
	if version == "" {
		version = DefaultVersion
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	doc, ok := r.docs[registryKey(kind, version)]
	if !ok {
		return Document{}, fmt.Errorf("%w: %s/%s", ErrUnknownPrompt, kind, version)
	}
	return doc, nil
}

// Kinds returns the registered agent kinds sorted alphabetically.
func (r *Registry) Kinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[string]bool)
	for _, doc := range r.docs {
		seen[doc.Kind] = true
	}
	kinds := make([]string, 0, len(seen))
	for kind := range seen {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

// Versions returns the registered versions for kind sorted alphabetically.
func (r *Registry) Versions(kind string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	prefix := kind + "/"
	versions := make([]string, 0)
	for key, doc := range r.docs {
		if strings.HasPrefix(key, prefix) {
			versions = append(versions, doc.Version)
		}
	}
	sort.Strings(versions)
	return versions
}

// Len returns the number of registered prompts.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.docs)
}

func registryKey(kind, version string) string {
	return kind + "/" + version
}
