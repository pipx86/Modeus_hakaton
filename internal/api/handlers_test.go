package api

import (
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/logging" // === NEW: Импорт логгера ===
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// === NEW: Инициализация логгера перед запусками тестов ===
func init() {
	logging.Init()
}

func do(t *testing.T, h http.Handler, method, path, role, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if role != "" {
		tok := auth.TokenFor("test-user", auth.Role(role))
		req.Header.Set("Authorization", "Bearer "+tok)
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
	rr := do(t, h, "POST", "/api/v1/runs", "teacher", `{"profession":"cook"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("ожидали 201, получили %d (%s)", rr.Code, rr.Body.String())
	}
	var run domain.Run
	if err := json.Unmarshal(rr.Body.Bytes(), &run); err != nil {
		t.Fatalf("тело не распарсилось как Run: %v", err)
	}
	if run.ID == "" || run.Profession != "cook" || run.Status != domain.RunPending || run.Stage != domain.StageCreated {
		t.Fatalf("неверный run: %+v", run)
	}
}

func TestCreateRunBadRequest(t *testing.T) {
	h := NewServer(newMemStore()).Routes()
	for _, body := range []string{`{"profession":""}`, `{}`, `{`} {
		rr := do(t, h, "POST", "/api/v1/runs", "teacher", body)
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

func TestCreateRunForbiddenStudent(t *testing.T) {
	h := NewServer(newMemStore()).Routes()
	rr := do(t, h, "POST", "/api/v1/runs", "student", `{"profession":"cook"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("student не должен создавать прогоны: ожидали 403, получили %d", rr.Code)
	}
}

func TestGetRun(t *testing.T) {
	m := newMemStore()
	m.seedRun(domain.Run{ID: "r1", Status: domain.RunPending, Profession: "cook", Stage: domain.StageCreated})
	h := NewServer(m).Routes()

	rr := do(t, h, "GET", "/api/v1/runs/r1", "student", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d", rr.Code)
	}
	var got domain.Run
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got.ID != "r1" || got.Profession != "cook" {
		t.Fatalf("вернулся не тот run: %+v", got)
	}

	rr = do(t, h, "GET", "/api/v1/runs/nope", "teacher", "")
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
	m.seedRun(domain.Run{ID: "r1", Status: domain.RunDone, Profession: "cook", Stage: domain.StageDone})
	m.seedNodes("r1", domain.Node{ID: "n1", Type: domain.NodeRole, Title: "Cook", Evidence: []domain.Evidence{{ID: "e1", SourceID: "s1", Quote: "q1"}}})
	h := NewServer(m).Routes()

	rr := do(t, h, "GET", "/api/v1/runs/r1/tree", "rop", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d", rr.Code)
	}

	rr = do(t, h, "GET", "/api/v1/runs/r1/tree", "student", "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("student не должен видеть tree: ожидали 403, получили %d", rr.Code)
	}
}

func TestGetRunAnswers(t *testing.T) {
	m := newMemStore()
	m.seedRun(domain.Run{ID: "r1", Status: domain.RunDone, Profession: "cook", Stage: domain.StageDone})
	m.seedEvidence(domain.Evidence{ID: "e1", SourceID: "s1", Quote: "q1"}, "r1")
	h := NewServer(m).Routes()

	rr := do(t, h, "GET", "/api/v1/runs/r1/answers", "teacher", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d", rr.Code)
	}

	rr = do(t, h, "GET", "/api/v1/runs/r1/answers", "student", "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("student не должен видеть answers: ожидали 403, получили %d", rr.Code)
	}
}

func TestApproveEvidence(t *testing.T) {
	m := newMemStore()
	m.seedRun(domain.Run{ID: "r1", Status: domain.RunDone, Profession: "cook", Stage: domain.StageDone})
	m.seedEvidence(domain.Evidence{ID: "e1", SourceID: "s1", Quote: "q1"}, "r1")
	h := NewServer(m).Routes()

	rr := do(t, h, "POST", "/api/v1/evidences/e1/approve", "teacher", "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("ожидали 204, получили %d", rr.Code)
	}

	rr = do(t, h, "POST", "/api/v1/evidences/e1/approve", "teacher", "")
	if rr.Code != http.StatusConflict {
		t.Fatalf("ожидали 409 на повтор, получили %d", rr.Code)
	}

	m2 := newMemStore()
	m2.seedRun(domain.Run{ID: "r2", Status: domain.RunDone, Profession: "dev", Stage: domain.StageDone})
	m2.seedEvidence(domain.Evidence{ID: "e2", SourceID: "s2", Quote: "q2"}, "r2")
	h2 := NewServer(m2).Routes()
	rr = do(t, h2, "POST", "/api/v1/evidences/e2/approve", "student", "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("student не должен утверждать: ожидали 403, получили %d", rr.Code)
	}
}

func TestExportRun(t *testing.T) {
	m := newMemStore()
	m.seedRun(domain.Run{ID: "done", Status: domain.RunDone, Profession: "cook", Stage: domain.StageDone})
	m.seedRun(domain.Run{ID: "pending", Status: domain.RunPending, Profession: "dev", Stage: domain.StageCreated})
	m.seedNodes("done", domain.Node{ID: "n1", Type: domain.NodeRole, Title: "Cook", Evidence: []domain.Evidence{{ID: "e1", SourceID: "s1", Quote: "q1"}}})
	h := NewServer(m).Routes()

	rr := do(t, h, "GET", "/api/v1/runs/done/export", "rop", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d", rr.Code)
	}

	rr = do(t, h, "GET", "/api/v1/runs/pending/export", "teacher", "")
	if rr.Code != http.StatusConflict {
		t.Fatalf("ожидали 409 на незавершённый, получили %d", rr.Code)
	}

	rr = do(t, h, "GET", "/api/v1/runs/done/export", "student", "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("student не должен экспортировать: ожидали 403, получили %d", rr.Code)
	}
}

func TestHealthPublic(t *testing.T) {
	h := NewServer(newMemStore()).Routes()
	rr := do(t, h, "GET", "/api/v1/health", "", "")
	if rr.Code != http.StatusOK || rr.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("unexpected health: %d %s", rr.Code, rr.Body.String())
	}
}

func TestIdempotentCreateRunRace(t *testing.T) {
	m := newMemStore()
	h := NewServer(m).Routes()

	const N = 5
	var wg sync.WaitGroup
	codes := make(chan int, N)
	bodies := make(chan string, N)
	start := make(chan struct{})

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			req := httptest.NewRequest("POST", "/api/v1/runs", strings.NewReader(`{"profession":"cook"}`))
			req.Header.Set("Authorization", "Bearer "+auth.TokenFor("u-race", auth.RoleTeacher))
			req.Header.Set("Idempotency-Key", "race-key-123")

			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			codes <- rr.Code
			b, _ := io.ReadAll(rr.Body)
			bodies <- string(b)
		}()
	}

	close(start)
	wg.Wait()
	close(codes)
	close(bodies)

	firstBody := ""
	for b := range bodies {
		if firstBody == "" {
			firstBody = b
		} else if b != firstBody {
			t.Fatalf("ответы в гонке разошлись: %q vs %q", firstBody, b)
		}
	}
	for c := range codes {
		if c != http.StatusCreated {
			t.Fatalf("все ответы обязаны быть 201, поймали %d", c)
		}
	}

	m.mu.RLock()
	count := len(m.runs)
	m.mu.RUnlock()

	if count != 1 {
		t.Fatalf("идемпотентность нарушена: ожидали 1 прогон, получили %d", count)
	}
}
func TestGetRunInvalidUUID(t *testing.T) {
	h := NewServer(newMemStore()).Routes()
	rr := do(t, h, "GET", "/api/v1/runs/not-a-uuid", "admin", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("ожидали 404 для невалидного UUID, получили %d", rr.Code)
	}
}

func TestExportRunNotFound(t *testing.T) {
	h := NewServer(newMemStore()).Routes()
	rr := do(t, h, "GET", "/api/v1/runs/nonexistent/export", "admin", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("ожидали 404 для экспорта несуществующего run, получили %d", rr.Code)
	}
}

func TestGetTreeNotFound(t *testing.T) {
	h := NewServer(newMemStore()).Routes()
	rr := do(t, h, "GET", "/api/v1/runs/nonexistent/tree", "admin", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("ожидали 404 для дерева несуществующего run, получили %d", rr.Code)
	}
}

func TestGetAnswersNotFound(t *testing.T) {
	h := NewServer(newMemStore()).Routes()
	rr := do(t, h, "GET", "/api/v1/runs/nonexistent/answers", "admin", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("ожидали 404 для answers несуществующего run, получили %d", rr.Code)
	}
}

func TestApproveEvidenceNotFound(t *testing.T) {
	h := NewServer(newMemStore()).Routes()
	rr := do(t, h, "POST", "/api/v1/evidences/nonexistent/approve", "teacher", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("ожидали 404 для approve несуществующего evidence, получили %d", rr.Code)
	}
}
