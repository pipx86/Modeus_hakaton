package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"activity-descriptor/internal/chat"
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

	if pg, ok := s.eventStore.(*sse.PGEventStore); ok {
		s.handlePostgresSSE(w, r, id, flusher, pg)
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

	var chatEvents <-chan chat.Event
	if s.chat != nil {
		stream, cancel, err := s.chat.Subscribe(id)
		if err == nil {
			chatEvents = stream
			defer cancel()
		}
	}

	ch := make(chan *sse.SSEEvent, 256)
	unsub := s.eventStore.SubscribeLive(func(ev *sse.SSEEvent) {
		select {
		case ch <- ev:
		default:
		}
	})
	defer unsub()
	seen := map[string]bool{}
	events, _ := s.eventStore.GetAfter(lastID, 1000)
	for _, ev := range events {
		if ev.RunID != id {
			continue
		}
		data, err := ev.Encode()
		if err != nil {
			continue
		}
		w.Write(data)
		seen[ev.ID] = true

		if ev.Type == sse.EventDone || ev.Type == sse.EventError {
			flusher.Flush()
			return
		}
	}
	flusher.Flush()

	ctx := r.Context()
	heartbeat := time.NewTicker(s.sseHeartbeat)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-ch:
			if ev.RunID != id || seen[ev.ID] {
				continue
			}
			seen[ev.ID] = true
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

		case chatEvent, ok := <-chatEvents:
			if !ok {
				chatEvents = nil
				continue
			}
			payload, err := json.Marshal(chatEvent)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", chatEvent.Type, payload); err != nil {
				return
			}
			flusher.Flush()
			if chatEvent.Type == "run_status" && (chatEvent.Status == "done" || chatEvent.Status == "degraded" || chatEvent.Status == "failed") {
				return
			}
		case <-heartbeat.C:
			// Heartbeat не меняет Last-Event-ID, потому что он не хранится в истории.
			if _, err := w.Write([]byte("event: heartbeat\ndata: {\"ping\":true}\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
