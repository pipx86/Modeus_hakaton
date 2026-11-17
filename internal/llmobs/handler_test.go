package llmobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/auth"
)

type fakeStore struct {
	requests    []Request
	request     Request
	prompts     []PromptVersion
	activatedID string
	activatedBy string
	reason      string
}

func (f *fakeStore) ListRequests(context.Context, ListFilter) ([]Request, error) {
	return f.requests, nil
}
func (f *fakeStore) GetRequest(context.Context, string) (Request, error) { return f.request, nil }
func (f *fakeStore) ListPromptVersions(context.Context, string) ([]PromptVersion, error) {
	return f.prompts, nil
}
func (f *fakeStore) ActivatePromptVersion(_ context.Context, id, by, reason string) error {
	f.activatedID, f.activatedBy, f.reason = id, by, reason
	return nil
}

func testServer(t *testing.T, store StoreAPI) *httptest.Server {
	t.Helper()
	auth.EnsureTestSecret()
	return httptest.NewServer(auth.Middleware(NewHandler(store)))
}

func request(t *testing.T, srv *httptest.Server, method, path, token, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, stringsReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if resp.Body != nil {
		_ = json.NewDecoder(resp.Body).Decode(&decoded)
	}
	return resp, decoded
}

func stringsReader(s string) *strings.Reader { return strings.NewReader(s) }

func TestHandlerRequiresAuth(t *testing.T) {
	srv := testServer(t, &fakeStore{})
	defer srv.Close()
	resp, _ := request(t, srv, http.MethodGet, "/api/v1/llm/requests", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestHandlerListsRequests(t *testing.T) {
	store := &fakeStore{requests: []Request{{
		ID: "r1", Agent: "analyst", Status: "ok", CreatedAt: time.Now(),
	}}}
	srv := testServer(t, store)
	defer srv.Close()

	resp, body := request(t, srv, http.MethodGet, "/api/v1/llm/requests?agent=analyst", auth.TokenFor("u1", auth.RoleTeacher), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %#v", body["items"])
	}
}

func TestHandlerGetsRequest(t *testing.T) {
	store := &fakeStore{request: Request{ID: "r1", Agent: "interviewer", Status: "ok"}}
	srv := testServer(t, store)
	defer srv.Close()

	resp, body := request(t, srv, http.MethodGet, "/api/v1/llm/requests/r1", auth.TokenFor("u1", auth.RoleStudent), "")
	if resp.StatusCode != http.StatusOK || body["id"] != "r1" {
		t.Fatalf("status = %d body=%#v", resp.StatusCode, body)
	}
}

func TestHandlerActivatesPromptAsTeacher(t *testing.T) {
	store := &fakeStore{}
	srv := testServer(t, store)
	defer srv.Close()

	resp, _ := request(t, srv, http.MethodPost, "/api/v1/llm/prompt-versions/activate",
		auth.TokenFor("teacher-1", auth.RoleTeacher), `{"id":"v1","reason":"test"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if store.activatedID != "v1" || store.activatedBy != "teacher-1" || store.reason != "test" {
		t.Fatalf("activation = %#v", store)
	}
}

func TestHandlerRejectsStudentActivation(t *testing.T) {
	srv := testServer(t, &fakeStore{})
	defer srv.Close()
	resp, _ := request(t, srv, http.MethodPost, "/api/v1/llm/prompt-versions/activate",
		auth.TokenFor("student-1", auth.RoleStudent), `{}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}
