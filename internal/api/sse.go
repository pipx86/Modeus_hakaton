package api

import (
	"net/http"
	"time"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/sse"
)

var stageOrder = []domain.Stage{
	domain.StageCreated,
	domain.StageInterview,
	domain.StageAnalysis,
	domain.StageSynthesis,
	domain.StageReview,
	domain.StageDone,
	domain.StageFailed,
}

var stagePct = map[domain.Stage]int{
	domain.StageCreated:   0,
	domain.StageInterview: 20,
	domain.StageAnalysis:  40,
	domain.StageSynthesis: 60,
	domain.StageReview:    80,
	domain.StageDone:      100,
	domain.StageFailed:    100,
}

func stageIndex(s domain.Stage) int {
	for i, x := range stageOrder {
		if x == s {
			return i + 1
		}
	}
	return 0
}

func isTerminal(s domain.Stage) bool {
	return s == domain.StageDone || s == domain.StageFailed
}

type sseEvent struct {
	Stage   domain.Stage `json:"stage"`
	Status  string       `json:"status"`
	Pct     int          `json:"pct"`
	Message string       `json:"message"`
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if _, err := s.store.GetRun(id); err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "run not found")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	lastID := ""
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		lastID = v
	}

	events, _ := s.eventStore.GetAfter(lastID, 100)
	for _, ev := range events {
		if ev.RunID != id {
			continue
		}
		data, err := ev.Encode()
		if err != nil {
			continue
		}
		w.Write(data)

		if ev.Type == sse.EventDone || ev.Type == sse.EventError {
			flusher.Flush()
			return
		}
	}
	flusher.Flush()

	ch := make(chan *sse.SSEEvent, 16)
	unsub := s.eventStore.SubscribeLive(func(ev *sse.SSEEvent) {
		select {
		case ch <- ev:
		default:
		}
	})
	defer unsub()

	ctx := r.Context()
	heartbeat := time.NewTicker(s.sseHeartbeat)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-ch:
			if ev.RunID != id {
				continue
			}
			data, err := ev.Encode()
			if err != nil {
				continue
			}
			if _, err := w.Write(data); err != nil {
				return
			}
			flusher.Flush()

			if ev.Type == sse.EventDone || ev.Type == sse.EventError {
				return
			}

		case <-heartbeat.C:
			hb := sse.NewEvent(id, sse.EventHeartbeat, map[string]any{"ping": true})
			data, err := hb.Encode()
			if err != nil {
				continue
			}
			if _, err := w.Write(data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
