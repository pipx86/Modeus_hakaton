package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/logging"
)

func init() {
	logging.Init()
}

func setupCRUDEndpointTest(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	m := newMemStore()
	_, _ = m.CreateRun("backend developer")
	srv := NewServer(m)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return srv, ts
}

func TestListRunsEmpty(t *testing.T) {
	_, ts := setupCRUDEndpointTest(t)
	auth.EnsureTestSecret()
	tok, _ := auth.Issue("admin-user", auth.RoleAdmin, time.Hour)

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/runs", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d", resp.StatusCode)
	}

	var runs []domain.Run
	json.NewDecoder(resp.Body).Decode(&runs)
	if len(runs) != 1 { // Один прогон создан в setup
		t.Fatalf("ожидали 1 прогон, получили %d", len(runs))
	}
}

func TestCreateSourceSuccess(t *testing.T) {
	_, ts := setupCRUDEndpointTest(t)
	auth.EnsureTestSecret()
	tok, _ := auth.Issue("teacher-user", auth.RoleTeacher, time.Hour)

	payload := map[string]any{
		"type":    "vacancy",
		"title":   "Senior Go Developer",
		"content": "We need someone who knows channels and goroutines.",
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/sources", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("ожидали 201, получили %d", resp.StatusCode)
	}

	var src domain.Source
	json.NewDecoder(resp.Body).Decode(&src)
	if src.Title != "Senior Go Developer" {
		t.Fatalf("title не сохранился: %q", src.Title)
	}
	if src.ID == "" {
		t.Fatal("ID источника не сгенерирован")
	}
}

func TestCreateSourceBadRequest(t *testing.T) {
	_, ts := setupCRUDEndpointTest(t)
	auth.EnsureTestSecret()
	tok, _ := auth.Issue("teacher-user", auth.RoleTeacher, time.Hour)

	payload := map[string]any{
		"type": "vacancy",
		// title отсутствует
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/sources", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("ожидали 400, получили %d", resp.StatusCode)
	}
}
