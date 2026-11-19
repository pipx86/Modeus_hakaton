package api

import (
	"errors"
	"net/http"
	"testing"

	"activity-descriptor/internal/chat"
)

// failingChatStub answers Messages/Post with a fixed error so the HTTP error
// mapping can be checked without a database or a real chat service.
type failingChatStub struct {
	getErr  error
	postErr error
}

func (f failingChatStub) Start(string, string) error { return nil }

func (f failingChatStub) Messages(string) ([]chat.Message, error) { return nil, f.getErr }

func (f failingChatStub) Post(string, string) (chat.Message, error) {
	return chat.Message{}, f.postErr
}

func (f failingChatStub) Subscribe(string) (<-chan chat.Event, func(), error) {
	return nil, nil, nil
}

func TestPostMessageMapsExplicitChatSessionErrors(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"finished", chat.ErrSessionFinished, http.StatusConflict, "SESSION_FINISHED"},
		{"interrupted", chat.ErrSessionInterrupted, http.StatusConflict, "SESSION_INTERRUPTED"},
		{"question pending", chat.ErrQuestionPending, http.StatusConflict, "CHAT_CONFLICT"},
		{"not found", chat.ErrNotFound, http.StatusNotFound, "NOT_FOUND"},
		{"empty", chat.ErrEmptyMessage, http.StatusBadRequest, "BAD_REQUEST"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			handler := NewServerWithChat(newMemStore(), failingChatStub{postErr: testCase.err}).Routes()
			rec := do(t, handler, http.MethodPost, "/api/v1/runs/run-1/messages", "teacher", `{"content":"ответ"}`)
			if rec.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, testCase.wantStatus, rec.Body.String())
			}
			if code := decodeErrorCode(t, rec.Body.Bytes()); code != testCase.wantCode {
				t.Fatalf("code = %q, want %q", code, testCase.wantCode)
			}
		})
	}
}

func TestGetMessagesErrorMapping(t *testing.T) {
	// The service itself serves persisted history for interrupted/finished runs;
	// only a missing run or a real storage failure reaches the handler as error.
	notFound := NewServerWithChat(newMemStore(), failingChatStub{getErr: chat.ErrNotFound}).Routes()
	if rec := do(t, notFound, http.MethodGet, "/api/v1/runs/run-1/messages", "student", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET missing run = %d, want 404", rec.Code)
	}
	broken := NewServerWithChat(newMemStore(), failingChatStub{getErr: errors.New("database unavailable")}).Routes()
	if rec := do(t, broken, http.MethodGet, "/api/v1/runs/run-1/messages", "student", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("GET with store error = %d, want 500", rec.Code)
	}
}
