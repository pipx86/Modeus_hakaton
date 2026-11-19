package api

import (
	"net/http"
	"testing"

	"activity-descriptor/internal/domain"
)

func TestZunRejectPersistsAndFilters(t *testing.T) {
	store := newMemStore()
	runID := zunSeedRun(t, store, "Аналитик данных",
		domain.Node{ID: "node-1", Type: domain.NodeTask, Title: "SQL-отчёты"},
		domain.Node{ID: "node-2", Type: domain.NodeTask, Title: "Дашборды"},
	)
	server := NewServer(store)

	reject := doMentorHandler(t, server.handleRejectZunSuggestion, http.MethodPost,
		"/api/v1/runs/"+runID+"/zun/suggestions/reject", "teacher",
		`{"node_id":"node-1"}`, map[string]string{"id": runID})
	if reject.Code != http.StatusOK {
		t.Fatalf("reject: ожидали 200, получили %d (%s)", reject.Code, reject.Body.String())
	}

	list := doMentorHandler(t, server.handleZunSuggestions, http.MethodGet,
		"/api/v1/runs/"+runID+"/zun/suggestions", "teacher", "", map[string]string{"id": runID})
	suggestions := decodeMentorJSON[[]zunSuggestion](t, list)
	if len(suggestions) != 1 || suggestions[0].NodeID != "node-2" {
		t.Fatalf("отклонённое предложение должно исчезнуть: %+v", suggestions)
	}

	// Повторный reject идемпотентен.
	again := doMentorHandler(t, server.handleRejectZunSuggestion, http.MethodPost,
		"/api/v1/runs/"+runID+"/zun/suggestions/reject", "teacher",
		`{"node_id":"node-1"}`, map[string]string{"id": runID})
	if again.Code != http.StatusOK {
		t.Fatalf("повторный reject: ожидали 200, получили %d (%s)", again.Code, again.Body.String())
	}
}

func TestZunRejectValidationAndAuth(t *testing.T) {
	store := newMemStore()
	runID := zunSeedRun(t, store, "Аналитик", domain.Node{ID: "node-1", Type: domain.NodeTask, Title: "Отчёты"})
	server := NewServer(store)

	student := doMentorHandler(t, server.handleRejectZunSuggestion, http.MethodPost,
		"/api/v1/runs/"+runID+"/zun/suggestions/reject", "student",
		`{"node_id":"node-1"}`, map[string]string{"id": runID})
	if student.Code != http.StatusForbidden {
		t.Fatalf("student: ожидали 403, получили %d", student.Code)
	}

	empty := doMentorHandler(t, server.handleRejectZunSuggestion, http.MethodPost,
		"/api/v1/runs/"+runID+"/zun/suggestions/reject", "teacher",
		`{}`, map[string]string{"id": runID})
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("пустой node_id: ожидали 400, получили %d", empty.Code)
	}

	missing := doMentorHandler(t, server.handleRejectZunSuggestion, http.MethodPost,
		"/api/v1/runs/"+runID+"/zun/suggestions/reject", "teacher",
		`{"node_id":"missing"}`, map[string]string{"id": runID})
	if missing.Code != http.StatusNotFound {
		t.Fatalf("чужой узел: ожидали 404, получили %d", missing.Code)
	}
}
