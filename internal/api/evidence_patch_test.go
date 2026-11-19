package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
)

func patchEvidenceQuote(t *testing.T, baseURL, token, id, quote string) *http.Response {
	t.Helper()
	body, err := json.Marshal(map[string]string{"quote": quote})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPatch, baseURL+"/api/v1/evidences/"+id, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

func TestUpdateEvidenceQuote(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	token, err := auth.Issue("teacher-user", auth.RoleAdmin, time.Hour)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	resp := patchEvidenceQuote(t, ts.URL, token, "ev1", "обновлённая цитата")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d", resp.StatusCode)
	}
	var updated domain.Evidence
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.Quote != "обновлённая цитата" {
		t.Fatalf("цитата не обновилась: %q", updated.Quote)
	}
}

func TestUpdateEvidenceQuoteRejectsEmpty(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	token, _ := auth.Issue("admin-user", auth.RoleAdmin, time.Hour)

	resp := patchEvidenceQuote(t, ts.URL, token, "ev1", "   ")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("ожидали 400, получили %d", resp.StatusCode)
	}
}

func TestUpdateEvidenceQuoteForbiddenForGuest(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	token, _ := auth.Issue("guest-user", auth.RoleUser, time.Hour)

	resp := patchEvidenceQuote(t, ts.URL, token, "ev1", "нельзя")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ожидали 403, получили %d", resp.StatusCode)
	}
}

func TestUpdateEvidenceQuoteNotFound(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	token, _ := auth.Issue("admin-user", auth.RoleAdmin, time.Hour)

	resp := patchEvidenceQuote(t, ts.URL, token, "missing", "цитата")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ожидали 404, получили %d", resp.StatusCode)
	}
}
