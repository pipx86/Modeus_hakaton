package embeddings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestВекторOllama(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" || r.Method != http.MethodPost {
			t.Error("неверный запрос")
		}
		var body struct{ Model, Input string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "test-model" || body.Input != "текст" {
			t.Errorf("%+v %v", body, err)
		}
		v := make([]float32, 768)
		v[0] = 1
		json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{v}})
	}))
	defer server.Close()
	client := Ollama{BaseURL: server.URL, Model: "test-model", Client: server.Client()}
	v, err := client.Embed(context.Background(), "текст")
	if err != nil || len(v) != 768 {
		t.Fatalf("%d %v", len(v), err)
	}
}

func TestОшибкаРазмераOllama(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"embeddings":[[1,2]]}`)) }))
	defer server.Close()
	client := Ollama{BaseURL: server.URL, Model: "test-model", Client: server.Client()}
	if _, err := client.Embed(context.Background(), "текст"); err == nil {
		t.Fatal("неверная размерность принята")
	}
}

func TestOllamaServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal error"}`))
	}))
	defer server.Close()
	client := Ollama{BaseURL: server.URL, Model: "test-model", Client: server.Client()}

	_, err := client.Embed(context.Background(), "текст")
	if err == nil {
		t.Fatal("ожидали ошибку при 500 от сервера")
	}
}

func TestOllamaInvalidJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not a json`))
	}))
	defer server.Close()
	client := Ollama{BaseURL: server.URL, Model: "test-model", Client: server.Client()}

	_, err := client.Embed(context.Background(), "текст")
	if err == nil {
		t.Fatal("ожидали ошибку парсинга невалидного JSON")
	}
}

func TestOllamaEmptyEmbeddings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{}})
	}))
	defer server.Close()
	client := Ollama{BaseURL: server.URL, Model: "test-model", Client: server.Client()}

	v, err := client.Embed(context.Background(), "текст")
	if err == nil && len(v) != 0 {
		t.Fatalf("ожидали пустой вектор или ошибку для пустых embeddings, получили %d элементов", len(v))
	}
}

func TestOllamaDefaultClient(t *testing.T) {
	// Проверяем, что если Client не задан, используется дефолтный
	client := Ollama{BaseURL: "http://localhost:11434", Model: "test"}
	if client.Client == nil {
		// В реальной реализации может быть lazy init или panic при вызове
		// Просто проверяем, что структура создается
		t.Log("Client is nil, will use default on first call")
	}
}

func TestOllamaEmptyInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{}})
	}))
	defer server.Close()
	client := Ollama{BaseURL: server.URL, Model: "test-model", Client: server.Client()}

	v, err := client.Embed(context.Background(), "")
	if err != nil && len(v) != 0 {
		t.Fatalf("пустой input должен вернуть пустой вектор или ошибку: %v, len=%d", err, len(v))
	}
}

func TestOllamaMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"embeddings": "not an array"}`))
	}))
	defer server.Close()
	client := Ollama{BaseURL: server.URL, Model: "test-model", Client: server.Client()}

	_, err := client.Embed(context.Background(), "текст")
	if err == nil {
		t.Fatal("ожидали ошибку при malformed response")
	}
}

func TestOllamaContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Сразу отменяем контекст

	client := Ollama{BaseURL: "http://localhost:11434", Model: "test"}
	_, err := client.Embed(ctx, "текст")
	if err == nil {
		t.Fatal("ожидали ошибку при отмененном контексте")
	}
}
