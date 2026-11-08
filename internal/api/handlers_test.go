package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"activity-descriptor/internal/domain"
)

func do(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", token)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func (m *memStore) seedRun(r domain.Run) {
	m.mu.Lock()
	defer m.mu.Unlock()

	copyRun := r
	m.runs[r.ID] = &copyRun
}

func (m *memStore) seedNodes(runID string, nodes ...domain.Node) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nodes[runID] = append(m.nodes[runID], nodes...)
}

func (m *memStore) seedEvidence(ev domain.Evidence, runID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.evidences[ev.ID] = ev
	m.runEvidenceIDs[runID] = append(m.runEvidenceIDs[runID], ev.ID)
}

func TestCreateRun(t *testing.T) {
	h := NewServer(newMemStore()).Routes()

	rr := do(t, h, "POST", "/api/v1/runs", "Bearer t", `{"profession":"cook"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("ожидали 201, получили %d (%s)", rr.Code, rr.Body.String())
	}

	var run domain.Run
	if err := json.Unmarshal(rr.Body.Bytes(), &run); err != nil {
		t.Fatalf("тело не распарсилось как Run: %v", err)
	}

	if run.ID == "" {
		t.Fatalf("пустой id")
	}
	if run.Profession != "cook" {
		t.Fatalf("неверная profession: %s", run.Profession)
	}
	if run.Status != domain.RunPending {
		t.Fatalf("неверный status: %s", run.Status)
	}
	if run.Stage != domain.StageCreated {
		t.Fatalf("неверный stage: %s", run.Stage)
	}
}

func TestCreateRunBadRequest(t *testing.T) {
	h := NewServer(newMemStore()).Routes()

	bodies := []string{
		`{"profession":""}`,
		`{}`,
		`{`,
	}

	for _, body := range bodies {
		rr := do(t, h, "POST", "/api/v1/runs", "Bearer t", body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body %q: ожидали 400, получили %d", body, rr.Code)
		}
	}
}

func TestCreateRunUnauthorized(t *testing.T) {
	h := NewServer(newMemStore()).Routes()

	rr := do(t, h, "POST", "/api/v1/runs", "", `{"profession":"cook"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("ожидали 401, получили %d", rr.Code)
	}
}

func TestGetRun(t *testing.T) {
	m := newMemStore()
	m.seedRun(domain.Run{
		ID:         "r1",
		Status:     domain.RunPending,
		Profession: "cook",
		Stage:      domain.StageCreated,
	})
	h := NewServer(m).Routes()

	rr := do(t, h, "GET", "/api/v1/runs/r1", "Bearer t", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d", rr.Code)
	}

	var got domain.Run
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("тело не распарсилось как Run: %v", err)
	}
	if got.ID != "r1" || got.Profession != "cook" || got.Status != domain.RunPending {
		t.Fatalf("вернулся не тот run: %+v", got)
	}

	rr = do(t, h, "GET", "/api/v1/runs/nope", "Bearer t", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("ожидали 404, получили %d", rr.Code)
	}

	rr = do(t, h, "GET", "/api/v1/runs/r1", "", "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("ожидали 401, получили %d", rr.Code)
	}
}

func TestGetRunTree(t *testing.T) {
	m := newMemStore()
	m.seedRun(domain.Run{
		ID:         "r1",
		Status:     domain.RunDone,
		Profession: "cook",
		Stage:      domain.StageDone,
	})
	m.seedRun(domain.Run{
		ID:         "empty",
		Status:     domain.RunPending,
		Profession: "dev",
		Stage:      domain.StageCreated,
	})
	m.seedNodes("r1", domain.Node{
		ID:    "n1",
		Type:  domain.NodeRole,
		Title: "Cook",
		Evidence: []domain.Evidence{
			{ID: "e1", SourceID: "s1", Quote: "q1"},
		},
	})

	h := NewServer(m).Routes()

	rr := do(t, h, "GET", "/api/v1/runs/r1/tree", "Bearer t", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d", rr.Code)
	}

	var nodes []domain.Node
	if err := json.Unmarshal(rr.Body.Bytes(), &nodes); err != nil {
		t.Fatalf("тело не распарсилось как []Node: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != "n1" {
		t.Fatalf("неверное дерево: %+v", nodes)
	}

	rr = do(t, h, "GET", "/api/v1/runs/empty/tree", "Bearer t", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("ожидали 200 для пустого дерева, получили %d", rr.Code)
	}
	nodes = nil
	if err := json.Unmarshal(rr.Body.Bytes(), &nodes); err != nil {
		t.Fatalf("пустое дерево не распарсилось: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("ожидали пустой массив, получили %+v", nodes)
	}

	rr = do(t, h, "GET", "/api/v1/runs/nope/tree", "Bearer t", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("ожидали 404, получили %d", rr.Code)
	}

	rr = do(t, h, "GET", "/api/v1/runs/r1/tree", "", "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("ожидали 401, получили %d", rr.Code)
	}
}

func TestGetRunAnswers(t *testing.T) {
	m := newMemStore()
	m.seedRun(domain.Run{
		ID:         "r1",
		Status:     domain.RunDone,
		Profession: "cook",
		Stage:      domain.StageDone,
	})
	m.seedEvidence(domain.Evidence{
		ID:       "e1",
		SourceID: "s1",
		Quote:    "q1",
	}, "r1")

	h := NewServer(m).Routes()

	rr := do(t, h, "GET", "/api/v1/runs/r1/answers", "Bearer t", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d", rr.Code)
	}

	var answers []domain.Evidence
	if err := json.Unmarshal(rr.Body.Bytes(), &answers); err != nil {
		t.Fatalf("тело не распарсилось как []Evidence: %v", err)
	}
	if len(answers) != 1 || answers[0].ID != "e1" {
		t.Fatalf("неверные answers: %+v", answers)
	}

	rr = do(t, h, "GET", "/api/v1/runs/nope/answers", "Bearer t", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("ожидали 404, получили %d", rr.Code)
	}

	rr = do(t, h, "GET", "/api/v1/runs/r1/answers", "", "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("ожидали 401, получили %d", rr.Code)
	}
}

func TestApproveEvidence(t *testing.T) {
	m := newMemStore()
	m.seedRun(domain.Run{
		ID:         "r1",
		Status:     domain.RunDone,
		Profession: "cook",
		Stage:      domain.StageDone,
	})
	m.seedEvidence(domain.Evidence{
		ID:       "e1",
		SourceID: "s1",
		Quote:    "q1",
	}, "r1")

	h := NewServer(m).Routes()

	rr := do(t, h, "POST", "/api/v1/evidences/e1/approve", "Bearer t", "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("ожидали 204, получили %d", rr.Code)
	}

	rr = do(t, h, "POST", "/api/v1/evidences/e1/approve", "Bearer t", "")
	if rr.Code != http.StatusConflict {
		t.Fatalf("ожидали 409 на повторный approve, получили %d", rr.Code)
	}

	rr = do(t, h, "POST", "/api/v1/evidences/nope/approve", "Bearer t", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("ожидали 404, получили %d", rr.Code)
	}

	rr = do(t, h, "POST", "/api/v1/evidences/e1/approve", "", "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("ожидали 401, получили %d", rr.Code)
	}
}

func TestExportRun(t *testing.T) {
	m := newMemStore()
	m.seedRun(domain.Run{
		ID:         "done",
		Status:     domain.RunDone,
		Profession: "cook",
		Stage:      domain.StageDone,
	})
	m.seedRun(domain.Run{
		ID:         "pending",
		Status:     domain.RunPending,
		Profession: "dev",
		Stage:      domain.StageCreated,
	})
	m.seedNodes("done", domain.Node{
		ID:    "n1",
		Type:  domain.NodeRole,
		Title: "Cook",
		Evidence: []domain.Evidence{
			{ID: "e1", SourceID: "s1", Quote: "q1"},
		},
	})

	h := NewServer(m).Routes()

	rr := do(t, h, "GET", "/api/v1/runs/done/export", "Bearer t", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d", rr.Code)
	}

	var nodes []domain.Node
	if err := json.Unmarshal(rr.Body.Bytes(), &nodes); err != nil {
		t.Fatalf("тело не распарсилось как []Node: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != "n1" {
		t.Fatalf("неверный export: %+v", nodes)
	}

	rr = do(t, h, "GET", "/api/v1/runs/pending/export", "Bearer t", "")
	if rr.Code != http.StatusConflict {
		t.Fatalf("ожидали 409 на незавершённый run, получили %d", rr.Code)
	}

	rr = do(t, h, "GET", "/api/v1/runs/nope/export", "Bearer t", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("ожидали 404, получили %d", rr.Code)
	}

	rr = do(t, h, "GET", "/api/v1/runs/done/export", "", "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("ожидали 401, получили %d", rr.Code)
	}
}

func TestHealthPublic(t *testing.T) {
	h := NewServer(newMemStore()).Routes()
	rr := do(t, h, "GET", "/api/v1/health", "", "")
	if rr.Code != http.StatusOK || rr.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("unexpected health: %d %s", rr.Code, rr.Body.String())
	}
}
