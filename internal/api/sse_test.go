package api

import (
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/logging"
	"activity-descriptor/internal/sse"
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func init() {
	logging.Init()
}

func collectSSE(br *bufio.Reader) (ids []string, events []string, termErr error) {
	curID := ""
	for {
		ln, err := br.ReadString('\n')
		if err != nil {
			termErr = err
			return
		}
		t := strings.TrimRight(ln, "\r\n")
		switch {
		case strings.HasPrefix(t, "id: "):
			curID = strings.TrimPrefix(t, "id: ")
		case strings.HasPrefix(t, "event: "):
			events = append(events, strings.TrimPrefix(t, "event: "))
		case strings.HasPrefix(t, "data: "):
			ids = append(ids, curID)
		}
	}
}

func readOneEvent(t *testing.T, br *bufio.Reader) (id, eventType, data string) {
	t.Helper()
	for {
		ln, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("поток оборвался до события: %v", err)
		}
		line := strings.TrimRight(ln, "\r\n")
		if strings.HasPrefix(line, "id: ") {
			id = strings.TrimPrefix(line, "id: ")
		}
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			return id, eventType, strings.TrimPrefix(line, "data: ")
		}
	}
}

func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline") || strings.Contains(msg, "canceled") || strings.Contains(msg, "context canceled")
}

func doStream(t *testing.T, url, role string, lastEventID string, timeout time.Duration) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		cancel()
		t.Fatalf("сборка запроса: %v", err)
	}
	if role != "" {
		auth.EnsureTestSecret()
		tok, _ := auth.Issue("test-user", auth.Role(role), time.Hour)
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("запрос упал: %v", err)
	}
	return resp, cancel
}

func seedRun(m *memStore, r domain.Run) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := r
	m.runs[r.ID] = &cp
}

func TestSSEStreamToDone(t *testing.T) {
	m := newMemStore()
	seedRun(m, domain.Run{ID: "r1", Status: domain.RunRunning, Profession: "cook", Stage: domain.StageCreated})
	srv := NewServer(m)
	srv.sseHeartbeat = time.Hour
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, cancel := doStream(t, ts.URL+"/api/v1/runs/r1/events", "teacher", "", 3*time.Second)
	defer cancel()
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("ожидали text/event-stream, получили %q", ct)
	}

	time.Sleep(50 * time.Millisecond)

	go func() {
		time.Sleep(80 * time.Millisecond)
		_ = srv.eventStore.Append(sse.NewEvent("r1", sse.EventStageChange, map[string]string{"stage": "interview"}))
		time.Sleep(40 * time.Millisecond)
		_ = srv.eventStore.Append(sse.NewEvent("r1", sse.EventStageChange, map[string]string{"stage": "analysis"}))
		time.Sleep(40 * time.Millisecond)
		_ = srv.eventStore.Append(sse.NewEvent("r1", sse.EventDone, map[string]string{"stage": "done"}))
	}()

	br := bufio.NewReader(resp.Body)
	var seen []string
	for i := 0; i < 3; i++ {
		_, _, data := readOneEvent(t, br)
		seen = append(seen, data)
	}
	if !strings.Contains(seen[0], `"stage":"interview"`) {
		t.Fatalf("первое событие не interview: %s", seen[0])
	}
	if !strings.Contains(seen[1], `"stage":"analysis"`) {
		t.Fatalf("второе событие не analysis: %s", seen[1])
	}
	if !strings.Contains(seen[2], `"stage":"done"`) {
		t.Fatalf("третье событие не done: %s", seen[2])
	}
}

func TestSSELastEventIDNoDupes(t *testing.T) {
	m := newMemStore()
	seedRun(m, domain.Run{ID: "r1", Status: domain.RunRunning, Profession: "cook", Stage: domain.StageAnalysis})
	srv := NewServer(m)
	srv.sseHeartbeat = time.Hour
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	e1 := sse.NewEvent("r1", sse.EventStageChange, map[string]string{"stage": "interview"})
	e2 := sse.NewEvent("r1", sse.EventStageChange, map[string]string{"stage": "analysis"})
	e3 := sse.NewEvent("r1", sse.EventStageChange, map[string]string{"stage": "review"})
	_ = srv.eventStore.Append(e1)
	_ = srv.eventStore.Append(e2)
	_ = srv.eventStore.Append(e3)

	resp, cancel := doStream(t, ts.URL+"/api/v1/runs/r1/events", "teacher", e2.ID, 2*time.Second)
	defer cancel()
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	id, _, data := readOneEvent(t, br)
	if id == e1.ID || id == e2.ID {
		t.Fatalf("дедуп нарушен: ожидали только события после %s, получили id=%s body=%s", e2.ID, id, data)
	}
	if !strings.Contains(data, `"stage":"review"`) {
		t.Fatalf("после Last-Event-ID должен прийти review, получили: %s", data)
	}
}

func TestSSETerminalClosesImmediately(t *testing.T) {
	m := newMemStore()
	seedRun(m, domain.Run{ID: "r1", Status: domain.RunDone, Profession: "cook", Stage: domain.StageDone})
	srv := NewServer(m)
	srv.sseHeartbeat = time.Hour
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	doneEv := sse.NewEvent("r1", sse.EventDone, map[string]string{"stage": "done"})
	_ = srv.eventStore.Append(doneEv)

	resp, cancel := doStream(t, ts.URL+"/api/v1/runs/r1/events", "teacher", "", 2*time.Second)
	defer cancel()
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	id, _, data := readOneEvent(t, br)
	if id != doneEv.ID || !strings.Contains(data, `"stage":"done"`) {
		t.Fatalf("ожидали одно событие done, получили id=%s data=%s", id, data)
	}
}

func TestSSEHeartbeat(t *testing.T) {
	m := newMemStore()
	seedRun(m, domain.Run{ID: "r1", Status: domain.RunRunning, Profession: "cook", Stage: domain.StageCreated})
	srv := NewServer(m)
	srv.sseHeartbeat = 30 * time.Millisecond
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, cancel := doStream(t, ts.URL+"/api/v1/runs/r1/events", "teacher", "", 2*time.Second)
	defer cancel()
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	seenHB := false
	for i := 0; i < 50 && !seenHB; i++ {
		_, evType, _ := readOneEvent(t, br)
		if evType == string(sse.EventHeartbeat) {
			seenHB = true
		}
	}
	if !seenHB {
		t.Fatalf("heartbeat не пришёл за 50 событий")
	}
}

func TestSSEAuthAnd404(t *testing.T) {
	m := newMemStore()
	seedRun(m, domain.Run{ID: "r1", Status: domain.RunRunning, Profession: "cook", Stage: domain.StageCreated})
	h := NewServer(m).Routes()

	rr := do(t, h, "GET", "/api/v1/runs/r1/events", "", "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("ожидали 401, получили %d", rr.Code)
	}

	rr = do(t, h, "GET", "/api/v1/runs/nope/events", "teacher", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("ожидали 404, получили %d", rr.Code)
	}
}
