package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
)

func TestInterviewContinuationStartsAnalysisOnExplicitFinish(t *testing.T) {
	const runID = "continuation-run"
	store := newMemStore()
	store.seedRun(domain.Run{ID: runID, CreatedBy: "test-user", Profession: "Аналитик данных", Status: domain.RunRunning})
	service := chat.NewService(persistentInterviewer{}, nil)
	handler := NewServerWithChat(store, service).Routes()
	if err := service.Start(runID, "Аналитик данных"); err != nil {
		t.Fatal(err)
	}
	runPath := "/api/v1/runs/" + runID
	for i := 0; i < 4; i++ {
		waitForChatMessages(t, handler, runID, 2*i+2)
		rec := do(t, handler, http.MethodPost, runPath+"/messages", "user", `{"content":"Готовлю отчёты."}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("answer %d = %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := do(t, handler, http.MethodGet, runPath+"/interview-progress", "user", "")
	var progress chat.InterviewProgress
	if err := json.Unmarshal(rec.Body.Bytes(), &progress); rec.Code != http.StatusOK || err != nil ||
		!progress.AwaitingContinuation || progress.Finishing {
		t.Fatalf("expected pending choice, got %d: %s", rec.Code, rec.Body.String())
	}
	path := runPath + "/interview-continuation"
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"continue":false}`))
	request.Header.Set("Authorization", "Bearer "+auth.TokenFor("another-user", auth.RoleUser))
	foreign := httptest.NewRecorder()
	handler.ServeHTTP(foreign, request)
	if foreign.Code != http.StatusForbidden {
		t.Fatalf("choice for another user's interview = %d, want 403", foreign.Code)
	}
	if rec := do(t, handler, http.MethodPost, path, "user", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty choice = %d, want 400", rec.Code)
	}
	if rec := do(t, handler, http.MethodPost, path, "user", `{"continue":false}`); rec.Code != http.StatusNoContent {
		t.Fatalf("finish choice = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, handler, http.MethodPost, path, "user", `{"continue":true}`); rec.Code != http.StatusConflict {
		t.Fatalf("second choice = %d, want 409", rec.Code)
	}
	waitForChatMessages(t, handler, runID, 10)
}
