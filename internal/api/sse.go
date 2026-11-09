package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"activity-descriptor/internal/domain"
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
	w.WriteHeader(http.StatusOK)

	lastID := 0
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			lastID = n
		}
	}

	ctx := r.Context()
	tick := time.NewTicker(s.ssePoll)
	defer tick.Stop()
	beat := time.NewTicker(s.sseHeartbeat)
	defer beat.Stop()

	send := func(ev sseEvent, sseID int) {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "id: %d\nevent: stage\ndata: %s\n\n", sseID, data)
		flusher.Flush()
	}

	run, _ := s.store.GetRun(id)
	cur := run.Stage
	if idx := stageIndex(cur); idx > lastID {
		send(sseEvent{Stage: cur, Status: string(run.Status), Pct: stagePct[cur], Message: string(cur)}, idx)
		lastID = idx
	}
	if isTerminal(cur) {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-beat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-tick.C:
			run, err := s.store.GetRun(id)
			if err != nil {
				return
			}
			ns := run.Stage
			idx := stageIndex(ns)
			if idx > lastID {
				send(sseEvent{Stage: ns, Status: string(run.Status), Pct: stagePct[ns], Message: string(ns)}, idx)
				lastID = idx
			}
			if isTerminal(ns) {
				return
			}
		}
	}
}
