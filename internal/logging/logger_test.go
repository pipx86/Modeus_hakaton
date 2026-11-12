package logging

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestLoggerPreservesStreaming(t *testing.T) {
	flushed := false
	handler := RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("обёртка не поддерживает поток")
			return
		}
		f.Flush()
		flushed = true
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if !flushed || !recorder.Flushed {
		t.Fatal("данные не переданы клиенту")
	}
}
