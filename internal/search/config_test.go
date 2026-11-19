package search

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestConfigFromEnvDefaults(t *testing.T) {
	t.Setenv("OLLAMA_URL", "")
	t.Setenv("OLLAMA_EMBED_MODEL", "")
	cfg := ConfigFromEnv()
	if cfg.OllamaURL != DefaultOllamaURL || cfg.EmbedModel != DefaultEmbedModel {
		t.Fatalf("умолчания: %+v", cfg)
	}
	client := cfg.Client()
	if client.BaseURL != DefaultOllamaURL || client.Model != DefaultEmbedModel {
		t.Fatalf("клиент: %+v", client)
	}
}

func TestConfigFromEnvOverridesAndTrims(t *testing.T) {
	t.Setenv("OLLAMA_URL", "  http://127.0.0.1:11434/  ")
	t.Setenv("OLLAMA_EMBED_MODEL", "  nomic-embed-text:v1.5  ")
	cfg := ConfigFromEnv()
	if cfg.OllamaURL != "http://127.0.0.1:11434" {
		t.Fatalf("адрес: %q", cfg.OllamaURL)
	}
	if cfg.EmbedModel != "nomic-embed-text:v1.5" {
		t.Fatalf("модель: %q", cfg.EmbedModel)
	}
}

func TestConfigServiceUsesPool(t *testing.T) {
	cfg := Config{OllamaURL: "http://example.test:11434", EmbedModel: "embed"}
	service := cfg.Service(nil)
	if service.Pool != nil || service.Model.BaseURL != cfg.OllamaURL || service.Model.Model != cfg.EmbedModel {
		t.Fatalf("сервис: %+v", service)
	}
}

func TestServiceWithoutPoolFailsClearly(t *testing.T) {
	var id pgtype.UUID
	id.Bytes[15] = 1
	id.Valid = true
	service := Service{}
	if err := service.IndexSource(context.Background(), id); err == nil || !strings.Contains(err.Error(), "PostgreSQL") {
		t.Fatalf("IndexSource: %v", err)
	}
	if _, _, err := service.Search(context.Background(), id, "запрос", 5); err == nil || !strings.Contains(err.Error(), "PostgreSQL") {
		t.Fatalf("Search: %v", err)
	}
	if _, _, err := service.HybridSearch(context.Background(), id, "запрос", 5); err == nil || !strings.Contains(err.Error(), "PostgreSQL") {
		t.Fatalf("HybridSearch: %v", err)
	}
}
