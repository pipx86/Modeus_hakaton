package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/domain"
)

func collectSSE(br *bufio.Reader) (ids []string, bodies []string, termErr error) {
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
		case strings.HasPrefix(t, "data: "):
			ids = append(ids, curID)
			bodies = append(bodies, strings.TrimPrefix(t, "data: "))
		}
	}
}

func readOneEvent(t *testing.T, br *bufio.Reader) (id, data string) {
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
		if strings.HasPrefix(line, "data: ") {
			return id, strings.TrimPrefix(line, "data: ")
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

func doStream(t *testing.T, url, lastEventID string, timeout time.Duration) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		cancel()
		t.Fatalf("сборка запроса: %v", err)
	}
	req.Header.Set("Authorization", "Bearer t")
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

func TestSSEStreamToDone(t *testing.T) {
	m := newMemStore()
	m.seedRun(domain.Run{ID: "r1", Status: domain.RunRunning, Profession: "cook", Stage: domain.StageCreated})
	srv := NewServer(m)
	srv.ssePoll = 40 * time.Millisecond
	srv.sseHeartbeat = time.Hour
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, cancel := doStream(t, ts.URL+"/api/v1/runs/r1/events", "", 3*time.Second)
	defer cancel()
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("ожидали text/event-stream, получили %q", ct)
	}

	go func() {
		for _, st := range []domain.Stage{domain.StageInterview, domain.StageAnalysis, domain.StageSynthesis, domain.StageReview, domain.StageDone} {
			time.Sleep(100 * time.Millisecond)
			_ = m.SetStage("r1", st)
		}
	}()

	ids, bodies, termErr := collectSSE(bufio.NewReader(resp.Body))
	want := []string{"1", "2", "3", "4", "5", "6"}
	if len(ids) != len(want) {
		t.Fatalf("ожидали 6 событий, получили %d: %v", len(ids), ids)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("событие %d: ожидали id=%s, получили %s", i, want[i], ids[i])
		}
	}
	if !strings.Contains(bodies[5], `"stage":"done"`) {
		t.Fatalf("последнее событие не done: %s", bodies[5])
	}
	if isTimeoutErr(termErr) {
		t.Fatalf("поток не закрылся на done (завис): %v", termErr)
	}
}

func TestSSELastEventIDNoDupes(t *testing.T) {
	m := newMemStore()
	m.seedRun(domain.Run{ID: "r1", Status: domain.RunRunning, Profession: "cook", Stage: domain.StageAnalysis})
	srv := NewServer(m)
	srv.ssePoll = 40 * time.Millisecond
	srv.sseHeartbeat = time.Hour
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	go func() {
		time.Sleep(80 * time.Millisecond)
		_ = m.SetStage("r1", domain.StageReview)
	}()

	resp, cancel := doStream(t, ts.URL+"/api/v1/runs/r1/events", "3", 2*time.Second)
	defer cancel()
	defer resp.Body.Close()

	id, body := readOneEvent(t, bufio.NewReader(resp.Body))
	if id != "5" {
		t.Fatalf("дедуп нарушен: ожидали id=5 (review), получили id=%s body=%s", id, body)
	}
	if strings.Contains(body, `"stage":"analysis"`) {
		t.Fatalf("analysis переотправлён как дубль: %s", body)
	}
}

func TestSSETerminalClosesImmediately(t *testing.T) {
	m := newMemStore()
	m.seedRun(domain.Run{ID: "r1", Status: domain.RunDone, Profession: "cook", Stage: domain.StageDone})
	srv := NewServer(m)
	srv.ssePoll = 40 * time.Millisecond
	srv.sseHeartbeat = time.Hour
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, cancel := doStream(t, ts.URL+"/api/v1/runs/r1/events", "", 2*time.Second)
	defer cancel()
	defer resp.Body.Close()

	ids, _, termErr := collectSSE(bufio.NewReader(resp.Body))
	if len(ids) != 1 || ids[0] != "6" {
		t.Fatalf("ожидали ровно одно событие done id=6, получили %v", ids)
	}
	if isTimeoutErr(termErr) {
		t.Fatalf("поток не закрылся на done: %v", termErr)
	}
}

func TestSSEHeartbeat(t *testing.T) {
	m := newMemStore()
	m.seedRun(domain.Run{ID: "r1", Status: domain.RunRunning, Profession: "cook", Stage: domain.StageCreated})
	srv := NewServer(m)
	srv.ssePoll = time.Hour
	srv.sseHeartbeat = 30 * time.Millisecond
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, cancel := doStream(t, ts.URL+"/api/v1/runs/r1/events", "", 2*time.Second)
	defer cancel()
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	seenPing := false
	for i := 0; i < 50 && !seenPing; i++ {
		ln, err := br.ReadString('\n')
		if err != nil {
			break
		}
		if strings.TrimRight(ln, "\r\n") == ": ping" {
			seenPing = true
		}
	}
	if !seenPing {
		t.Fatalf("heartbeat не пришёл за 50 строк")
	}
}

func TestSSEAuthAnd404(t *testing.T) {
	m := newMemStore()
	m.seedRun(domain.Run{ID: "r1", Status: domain.RunRunning, Profession: "cook", Stage: domain.StageCreated})
	h := NewServer(m).Routes()

	rr := do(t, h, "GET", "/api/v1/runs/r1/events", "", "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("ожидали 401, получили %d", rr.Code)
	}

	rr = do(t, h, "GET", "/api/v1/runs/nope/events", "Bearer t", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("ожидали 404, получили %d", rr.Code)
	}
}
