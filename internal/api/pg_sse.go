package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/sse"
)

// История читается страницами для одного исследования, а новые записи опрашиваются из БД.
func (s *Server) handlePostgresSSE(w http.ResponseWriter, r *http.Request, runID string, flusher http.Flusher, store *sse.PGEventStore) {
	var chatEvents <-chan chat.Event
	if s.chat != nil {
		stream, cancel, err := s.chat.Subscribe(runID)
		if err == nil {
			chatEvents = stream
			defer cancel()
		}
	}
	lastID := r.Header.Get("Last-Event-ID")
	events, err := store.GetRunAfter(runID, lastID, 1000)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось прочитать историю событий")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	poll := time.NewTicker(s.ssePoll)
	defer poll.Stop()
	heartbeat := time.NewTicker(s.sseHeartbeat)
	defer heartbeat.Stop()
	for {
		for _, event := range events {
			data, err := event.Encode()
			if err != nil {
				return
			}
			if _, err = w.Write(data); err != nil {
				return
			}
			lastID = event.ID
			flusher.Flush()
			if event.Type == sse.EventDone || event.Type == sse.EventError {
				return
			}
		}
		// Если страница заполнена, сразу читаем следующую.
		if len(events) == 1000 {
			events, err = store.GetRunAfter(runID, lastID, 1000)
			if err != nil {
				return
			}
			continue
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
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
			if _, err := w.Write([]byte("event: heartbeat\ndata: {\"ping\":true}\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case <-poll.C:
			events, err = store.GetRunAfter(runID, lastID, 1000)
			if err != nil {
				return
			}
		}
		// Уже отправленные события не должны повторяться после heartbeat.
		if len(events) > 0 && events[len(events)-1].ID == lastID {
			events = nil
		}
	}
}
