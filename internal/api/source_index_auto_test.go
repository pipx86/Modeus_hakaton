package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/search"
)

// newIndexedServer builds a memStore-backed server with a fake background
// indexer so the create/upload handlers can be checked without PostgreSQL or
// Ollama. The fake reports every indexed source id on a buffered channel.
func newIndexedServer(t *testing.T) (*Server, *httptest.Server, <-chan string) {
	t.Helper()
	srv := NewServer(newMemStore())
	indexed := make(chan string, 4)
	var indexErr error
	srv.SetSourceIndexer(func(_ context.Context, sourceID string) error {
		indexed <- sourceID
		return indexErr
	})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return srv, ts, indexed
}

func TestCreateSourceStartsBackgroundIndexing(t *testing.T) {
	_, ts, indexed := newIndexedServer(t)
	auth.EnsureTestSecret()
	token, _ := auth.Issue("admin-user", auth.RoleAdmin, time.Hour)

	body, _ := json.Marshal(map[string]string{
		"type":    "document",
		"title":   "Материал с автоиндексацией",
		"content": "Аналитик собирает требования и проектирует отчёты.",
	})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/sources", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("ожидали 201, получили %d", resp.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil || created.ID == "" {
		t.Fatalf("источник без id: %v", err)
	}
	select {
	case got := <-indexed:
		if got != created.ID {
			t.Fatalf("проиндексирован %q, ожидали %q", got, created.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("создание источника не запустило фоновую индексацию")
	}
}

func TestUploadSourceStartsBackgroundIndexing(t *testing.T) {
	_, ts, indexed := newIndexedServer(t)
	auth.EnsureTestSecret()
	token, _ := auth.Issue("admin-user", auth.RoleAdmin, time.Hour)

	resp := uploadSource(t, ts.URL, token, "role.txt", []byte("Роль: аналитик данных"), map[string]string{"type": "standard"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("ожидали 201, получили %d", resp.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil || created.ID == "" {
		t.Fatalf("источник без id: %v", err)
	}
	select {
	case got := <-indexed:
		if got != created.ID {
			t.Fatalf("проиндексирован %q, ожидали %q", got, created.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("загрузка файла не запустила фоновую индексацию")
	}
}

func TestBackgroundIndexFailureDoesNotBreakCreate(t *testing.T) {
	srv := NewServer(newMemStore())
	failed := make(chan error, 1)
	srv.SetSourceIndexer(func(context.Context, string) error {
		err := errors.New("Ollama недоступна")
		failed <- err
		return err
	})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	auth.EnsureTestSecret()
	token, _ := auth.Issue("admin-user", auth.RoleAdmin, time.Hour)

	body, _ := json.Marshal(map[string]string{"type": "document", "title": "Источник", "content": "текст"})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/sources", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("ошибка фоновой индексации не должна ломать создание: %d", resp.StatusCode)
	}
	select {
	case <-failed:
	case <-time.After(3 * time.Second):
		t.Fatal("фоновый индексатор не вызван")
	}
}

func TestEnableSourceAutoIndexRequiresPostgres(t *testing.T) {
	srv := NewServer(newMemStore())
	if srv.EnableSourceAutoIndex() {
		t.Fatal("in-memory store не поддерживает индексацию")
	}
}

func TestIndexSourceEndpointValidation(t *testing.T) {
	srv := NewServer(newMemStore())
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	auth.EnsureTestSecret()
	admin, _ := auth.Issue("admin-user", auth.RoleAdmin, time.Hour)
	user, _ := auth.Issue("plain-user", auth.RoleUser, time.Hour)

	t.Run("некорректный UUID", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/sources/not-a-uuid/index", nil)
		req.Header.Set("Authorization", "Bearer "+admin)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("ожидали 400, получили %d", resp.StatusCode)
		}
	})
	t.Run("нужна роль admin", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/sources/11111111-1111-1111-1111-111111111111/index", nil)
		req.Header.Set("Authorization", "Bearer "+user)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("ожидали 403, получили %d", resp.StatusCode)
		}
	})
	t.Run("нужна PostgreSQL", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/sources/11111111-1111-1111-1111-111111111111/index", nil)
		req.Header.Set("Authorization", "Bearer "+admin)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotImplemented {
			t.Fatalf("ожидали 501, получили %d", resp.StatusCode)
		}
	})
}

func TestIndexSourceErrorMapping(t *testing.T) {
	cases := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{search.ErrSourceNotFound, http.StatusNotFound, "SOURCE_NOT_FOUND"},
		{search.ErrEmptySource, http.StatusUnprocessableEntity, "EMPTY_SOURCE"},
		{search.ErrSourceChanged, http.StatusConflict, "SOURCE_CHANGED"},
		{errors.New("Ollama вернула HTTP 503"), http.StatusBadGateway, "INDEX_FAILED"},
	}
	for _, tc := range cases {
		status, code, message := indexSourceError(tc.err)
		if status != tc.wantStatus || code != tc.wantCode {
			t.Fatalf("indexSourceError(%v) = %d %s", tc.err, status, code)
		}
		if strings.TrimSpace(message) == "" {
			t.Fatalf("пустое сообщение для %v", tc.err)
		}
		if tc.wantCode == "INDEX_FAILED" && !strings.Contains(message, tc.err.Error()) {
			t.Fatalf("сообщение %q не содержит причину %q", message, tc.err.Error())
		}
	}
}
