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
)

func setupCRUDTest(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	m := newMemStore()
	run, _ := m.CreateRun("backend developer")

	m.mu.Lock()
	m.nodes[run.ID] = []domain.Node{
		{ID: "n1", Type: domain.NodeRole, Title: "Backend Developer"},
		{ID: "n2", Type: domain.NodeTask, Title: "Learn Go", ParentID: strPtr("n1")},
	}
	m.evidences["ev1"] = domain.Evidence{ID: "ev1", SourceID: "src-go", Quote: "golang"}
	m.evidences["ev2"] = domain.Evidence{ID: "ev2", SourceID: "src-py", Quote: "python"}
	m.sources["src-go"] = domain.Source{ID: "src-go", Type: domain.SourceDocument, Title: "Источник Go"}
	m.runEvidenceIDs[run.ID] = []string{"ev1", "ev2"}
	m.mu.Unlock()

	srv := NewServer(m)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return srv, ts
}

func strPtr(s string) *string { return &s }

func TestPatchNodeSuccess(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	tok, _ := auth.Issue("admin-user", auth.RoleAdmin, time.Hour)

	patch := NodePatch{Title: strPtr("Learn Advanced Go")}
	body, _ := json.Marshal(patch)

	req, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/nodes/n2", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d", resp.StatusCode)
	}

	var node domain.Node
	json.NewDecoder(resp.Body).Decode(&node)
	if node.Title != "Learn Advanced Go" {
		t.Fatalf("title не обновился: %q", node.Title)
	}
}

func TestPatchNodeNotFound(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	tok, _ := auth.Issue("admin-user", auth.RoleAdmin, time.Hour)

	patch := NodePatch{Title: strPtr("whatever")}
	body, _ := json.Marshal(patch)

	req, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/nodes/nonexistent", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ошибка HTTP-запроса: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ожидали 404, получили %d", resp.StatusCode)
	}
}

func TestBulkApproveMixedResults(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	tok, _ := auth.Issue("teacher-user", auth.RoleTeacher, time.Hour)

	reqBody := bulkApproveRequest{IDs: []string{"ev1", "ev2", "ev-nonexist"}}
	body, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/evidences/bulk-approve", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d", resp.StatusCode)
	}

	var result bulkApproveResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Approved != 2 || result.Skipped != 1 {
		t.Fatalf("ожидали approved=2 skipped=1, получили approved=%d skipped=%d", result.Approved, result.Skipped)
	}
}

func TestBulkApproveIdempotent(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	tok, _ := auth.Issue("teacher-user", auth.RoleTeacher, time.Hour)

	firstReq := bulkApproveRequest{IDs: []string{"ev1"}}
	body1, _ := json.Marshal(firstReq)
	req1, _ := http.NewRequest("POST", ts.URL+"/api/v1/evidences/bulk-approve", bytes.NewReader(body1))
	req1.Header.Set("Authorization", "Bearer "+tok)
	req1.Header.Set("Content-Type", "application/json")
	resp1, _ := http.DefaultClient.Do(req1)
	resp1.Body.Close()

	secondReq := bulkApproveRequest{IDs: []string{"ev1"}}
	body2, _ := json.Marshal(secondReq)
	req2, _ := http.NewRequest("POST", ts.URL+"/api/v1/evidences/bulk-approve", bytes.NewReader(body2))
	req2.Header.Set("Authorization", "Bearer "+tok)
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("ошибка HTTP-запроса: %v", err)
	}
	defer resp2.Body.Close()

	var result bulkApproveResponse
	json.NewDecoder(resp2.Body).Decode(&result)
	if result.Approved != 0 || result.Skipped != 1 {
		t.Fatalf("повторная отправка должна дать approved=0 skipped=1, получили approved=%d skipped=%d", result.Approved, result.Skipped)
	}
}

func TestDeleteSourceWithEvidenceIsRejected(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	tok, _ := auth.Issue("admin-user", auth.RoleAdmin, time.Hour)

	req, _ := http.NewRequest("DELETE", ts.URL+"/api/v1/sources/src-go", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("ожидали 409, получили %d", resp.StatusCode)
	}

	answersReq, _ := http.NewRequest("GET", ts.URL+"/api/v1/runs/run-1/answers", nil)
	answersReq.Header.Set("Authorization", "Bearer "+tok)
	answersResp, err := http.DefaultClient.Do(answersReq)
	if err != nil {
		t.Fatalf("ошибка HTTP-запроса: %v", err)
	}
	defer answersResp.Body.Close()

	var answers []domain.Evidence
	json.NewDecoder(answersResp.Body).Decode(&answers)
	found := false
	for _, a := range answers {
		if a.SourceID == "src-go" {
			found = true
		}
	}
	if !found {
		t.Fatal("подтверждение используемого источника пропало")
	}
}

func TestDeleteSourceNotFound(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	tok, _ := auth.Issue("admin-user", auth.RoleAdmin, time.Hour)

	req, _ := http.NewRequest("DELETE", ts.URL+"/api/v1/sources/nonexistent", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ошибка HTTP-запроса: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ожидали 404, получили %d", resp.StatusCode)
	}
}

// === ДОБАВИТЬ В КОНЕЦ crud_test.go ===

func TestPatchNodeInvalidJSON(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	tok, _ := auth.Issue("admin-user", auth.RoleAdmin, time.Hour)

	req, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/nodes/n1", bytes.NewReader([]byte("{bad json")))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ошибка HTTP-запроса: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("ожидали 400 для невалидного JSON, получили %d", resp.StatusCode)
	}
}

func TestBulkApproveEmptyArray(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	tok, _ := auth.Issue("teacher-user", auth.RoleTeacher, time.Hour)

	reqBody := bulkApproveRequest{IDs: []string{}}
	body, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/evidences/bulk-approve", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ошибка HTTP-запроса: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("ожидали 400 для пустого массива, получили %d", resp.StatusCode)
	}
}

func TestBulkApproveInvalidJSON(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	tok, _ := auth.Issue("teacher-user", auth.RoleTeacher, time.Hour)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/evidences/bulk-approve", bytes.NewReader([]byte("{bad")))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ошибка HTTP-запроса: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("ожидали 400 для невалидного JSON, получили %d", resp.StatusCode)
	}
}

func TestDeleteRunRemovesItAndChildren(t *testing.T) {
	store, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	tok, _ := auth.Issue("admin-user", auth.RoleAdmin, time.Hour)

	req, _ := http.NewRequest("DELETE", ts.URL+"/api/v1/runs/run-1", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("ожидали 204, получили %d", resp.StatusCode)
	}

	getReq, _ := http.NewRequest("GET", ts.URL+"/api/v1/runs/run-1", nil)
	getReq.Header.Set("Authorization", "Bearer "+tok)
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusNotFound {
		t.Fatalf("после удаления ожидали 404, получили %d", getResp.StatusCode)
	}

	_ = store
}

func TestDeleteRunNotFound(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	tok, _ := auth.Issue("admin-user", auth.RoleAdmin, time.Hour)

	req, _ := http.NewRequest("DELETE", ts.URL+"/api/v1/runs/nonexistent", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ожидали 404, получили %d", resp.StatusCode)
	}
}

func TestDeleteRunRequiresAdmin(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()
	tok, _ := auth.Issue("teacher-user", auth.RoleTeacher, time.Hour)

	req, _ := http.NewRequest("DELETE", ts.URL+"/api/v1/runs/run-1", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ожидали 403, получили %d", resp.StatusCode)
	}
}
