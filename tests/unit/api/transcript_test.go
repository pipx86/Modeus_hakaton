package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"activity-descriptor/internal/agent/interviewer"
	"activity-descriptor/internal/api"
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
)

const exactAnswer = "  Запись «quotes»; О'Нил: <mark data-proof='literal'>& 3 < 7 \\ путь\n  "

func callAPI(h http.Handler, method, path, token string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	got := httptest.NewRecorder()
	h.ServeHTTP(got, req)
	return got
}

func testToken(t *testing.T, role auth.Role) string {
	t.Helper()
	auth.SetSecretForTests("test-only-token-secret-168434b-1")
	token, err := auth.Issue("transcript-test", role, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestInterviewKeepsOriginalAnswerInSession(t *testing.T) {
	agent, err := interviewer.New(chat.UnavailableClient{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := interviewer.NewSession(agent, "roundtrip", domain.Respondent{ID: "unit", Role: "Техник", Profession: "Техник"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = s.Answer(exactAnswer); err != nil {
		t.Fatal(err)
	}
	if got := s.State().Answers[0].Text; got != exactAnswer {
		t.Fatalf("ответ изменился: %q", got)
	}
}

func TestChatHTTPTransmitsExactText(t *testing.T) {
	token := testToken(t, auth.RoleTeacher)
	service := chat.NewService(chat.UnavailableClient{}, nil)
	if err := service.Start("literal-chat", "Техник"); err != nil {
		t.Fatal(err)
	}
	h := api.NewServerWithChat(api.NewMemoryStore(), service).Routes()
	path := "/api/v1/runs/literal-chat/messages"
	deadline := time.Now().Add(2 * time.Second)
	for {
		items, err := service.Messages("literal-chat")
		if err == nil && len(items) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("не появился первый вопрос")
		}
		time.Sleep(time.Millisecond)
	}
	bad := callAPI(h, http.MethodPost, path, token, []byte(`{"content":"Нуль: \u0000"}`))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("нулевой символ: %d", bad.Code)
	}
	data, _ := json.Marshal(map[string]string{"content": exactAnswer})
	got := callAPI(h, http.MethodPost, path, token, data)
	var message chat.Message
	if err := json.Unmarshal(got.Body.Bytes(), &message); got.Code != http.StatusCreated || err != nil || message.Content != exactAnswer {
		t.Fatalf("POST: %d %q %v", got.Code, message.Content, err)
	}
	found := false
	list := callAPI(h, http.MethodGet, path, token, nil)
	var history []chat.Message
	if err := json.Unmarshal(list.Body.Bytes(), &history); list.Code != http.StatusOK || err != nil {
		t.Fatalf("GET: %d %v", list.Code, err)
	}
	for _, item := range history {
		if item.ID == message.ID {
			found = item.Content == exactAnswer
		}
	}
	if !found {
		t.Fatal("в истории нет исходного ответа")
	}
}
