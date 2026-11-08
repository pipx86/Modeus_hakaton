package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendSignsExactBody(t *testing.T) {
	secret := []byte("local-test-secret")
	payload := []byte(`{"event":"run.completed","run_id":"123"}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write(body)
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || string(body) != string(payload) || r.Header.Get(SignatureHeader) != hex.EncodeToString(mac.Sum(nil)) {
			t.Error("request body, headers, method or signature differ")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	sender, err := NewSender(srv.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.Send(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
}

func TestSendRejectsFailureAndInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer srv.Close()
	sender, err := NewSender(srv.URL, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if sender.Send(context.Background(), []byte(`{}`)) == nil {
		t.Error("expected HTTP error")
	}
	if sender.Send(context.Background(), []byte(`not json`)) == nil {
		t.Error("expected JSON error")
	}
	if _, err := NewSender(srv.URL, nil); err == nil {
		t.Error("expected empty secret error")
	}
}
