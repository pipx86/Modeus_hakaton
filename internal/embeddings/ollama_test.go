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
