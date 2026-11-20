package api

import (
	"encoding/json"
	"net/http"
	"sort"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/chat"
)

type interviewABVariant struct {
	PromptVersion  string  `json:"prompt_version"`
	Sessions       int     `json:"sessions"`
	Completed      int     `json:"completed"`
	Questions      int     `json:"questions"`
	Answers        int     `json:"answers"`
	AverageQuality float64 `json:"average_quality"`
	CompletionRate float64 `json:"completion_rate"`
}

type interviewABStats struct {
	Variants []interviewABVariant `json:"variants"`
}

// handleInterviewABStats aggregates the durable interviewer sessions by prompt
// version. It lets the team compare the primary prompt with the B variant.
func (s *Server) handleInterviewABStats(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	pgStore, ok := s.store.(*PGStore)
	if !ok || pgStore.Pool == nil {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "статистика A/B требует PostgreSQL")
		return
	}
	rows, err := pgStore.Pool.Query(r.Context(), `SELECT prompt_version, status, state FROM run_chat_sessions`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось загрузить статистику интервью")
		return
	}
	defer rows.Close()
	type accumulator struct {
		interviewABVariant
		qualitySum int
	}
	byVersion := make(map[string]*accumulator)
	for rows.Next() {
		var version, status string
		var raw []byte
		if err := rows.Scan(&version, &status, &raw); err != nil {
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось прочитать статистику интервью")
			return
		}
		if version == "" {
			version = "default"
		}
		item := byVersion[version]
		if item == nil {
			item = &accumulator{interviewABVariant: interviewABVariant{PromptVersion: version}}
			byVersion[version] = item
		}
		var snapshot chat.SessionSnapshot
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			continue
		}
		answers := snapshot.Interviewer.State.Answers
		item.Sessions++
		item.Questions += len(snapshot.Interviewer.State.Questions)
		item.Answers += len(answers)
		for _, answer := range answers {
			item.qualitySum += answer.Quality
		}
		if status == string(chat.SessionCompleted) || snapshot.FinalStatus == "done" {
			item.Completed++
		}
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось прочитать статистику интервью")
		return
	}
	variants := make([]interviewABVariant, 0, len(byVersion))
	for _, item := range byVersion {
		if item.Answers > 0 {
			item.AverageQuality = float64(item.qualitySum) / float64(item.Answers)
		}
		if item.Sessions > 0 {
			item.CompletionRate = float64(item.Completed) / float64(item.Sessions) * 100
		}
		variants = append(variants, item.interviewABVariant)
	}
	sort.Slice(variants, func(i, j int) bool { return variants[i].PromptVersion < variants[j].PromptVersion })
	writeJSON(w, http.StatusOK, interviewABStats{Variants: variants})
}
