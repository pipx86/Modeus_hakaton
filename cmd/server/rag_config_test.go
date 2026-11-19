package main

import "testing"

func TestRAGSettingsDefaults(t *testing.T) {
	t.Setenv("OLLAMA_URL", "")
	t.Setenv("OLLAMA_EMBED_MODEL", "")
	t.Setenv("RAG_SEARCH_STRATEGY", "")
	t.Setenv("RAG_SEARCH_LIMIT", "")
	settings := ragSettingsFromEnv()
	if settings.OllamaURL != "http://127.0.0.1:11434" || settings.EmbedModel != "nomic-embed-text" {
		t.Fatalf("Ollama-умолчания: %+v", settings)
	}
	if settings.Strategy != "vector" || settings.Limit != 8 {
		t.Fatalf("стратегия/лимит: %+v", settings)
	}
}

func TestRAGSettingsFromEnv(t *testing.T) {
	t.Setenv("OLLAMA_URL", "http://127.0.0.1:12345")
	t.Setenv("OLLAMA_EMBED_MODEL", "custom-embed")
	t.Setenv("RAG_SEARCH_STRATEGY", "  HYBRID ")
	t.Setenv("RAG_SEARCH_LIMIT", "12")
	settings := ragSettingsFromEnv()
	if settings.OllamaURL != "http://127.0.0.1:12345" || settings.EmbedModel != "custom-embed" {
		t.Fatalf("Ollama: %+v", settings)
	}
	if settings.Strategy != "hybrid" || settings.Limit != 12 {
		t.Fatalf("стратегия/лимит: %+v", settings)
	}
}

func TestRAGSettingsInvalidValuesFallBack(t *testing.T) {
	t.Setenv("RAG_SEARCH_STRATEGY", "semantic-ish")
	t.Setenv("RAG_SEARCH_LIMIT", "500")
	settings := ragSettingsFromEnv()
	if settings.Strategy != "vector" {
		t.Fatalf("неизвестная стратегия должна откатываться на vector: %q", settings.Strategy)
	}
	if settings.Limit != 8 {
		t.Fatalf("недопустимый лимит должен откатываться на 8: %d", settings.Limit)
	}
}

func TestRAGSettingsLimitBounds(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
	}{{"1", 1}, {"50", 50}, {"0", 8}, {"51", 8}, {"abc", 8}, {"", 8}} {
		t.Setenv("RAG_SEARCH_LIMIT", tc.raw)
		if got := ragSettingsFromEnv().Limit; got != tc.want {
			t.Fatalf("RAG_SEARCH_LIMIT=%q: limit=%d, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestSourceAutoIndexEnabled(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{{"", true}, {"true", true}, {"1", true}, {"false", false}, {"0", false}, {"maybe", true}} {
		t.Setenv("SOURCE_AUTO_INDEX", tc.raw)
		if got := sourceAutoIndexEnabled(); got != tc.want {
			t.Fatalf("SOURCE_AUTO_INDEX=%q: got %v, want %v", tc.raw, got, tc.want)
		}
	}
}
