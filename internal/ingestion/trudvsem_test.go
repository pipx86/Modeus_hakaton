package ingestion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchTrudvsemUsesOpenDataFieldsWithoutContacts(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.URL.Query().Get("text"); got != "backend developer" {
			t.Errorf("text = %q", got)
		}
		if got := r.URL.Query().Get("limit"); got != "100" {
			t.Errorf("limit = %q", got)
		}
		if got := r.URL.Query().Get("offset"); got != "0" {
			t.Errorf("offset = %q", got)
		}
		_, _ = w.Write([]byte(`{"status":"200","meta":{"total":1},"results":{"vacancies":[{"vacancy":{"id":"tv-1","job-name":"Backend developer","vac_url":"https://trudvsem.example/v/1","creation-date":"2026-11-21","requirements":"Go, PostgreSQL","duty":"Разрабатывать API","contact_person":"Не сохранять","contact_list":[{"contact_value":"+7"}]}}]}}`))
	}))
	defer server.Close()

	items, err := FetchTrudvsem(context.Background(), server.Client(), server.URL, "backend developer", "Modeus test", 2)
	if err != nil {
		t.Fatalf("FetchTrudvsem: %v", err)
	}
	if requests != 1 || len(items) != 1 {
		t.Fatalf("requests=%d items=%d", requests, len(items))
	}
	if items[0].title() != "[Работа России] Backend developer" {
		t.Fatalf("title=%q", items[0].title())
	}
	if content := items[0].content(); content != "Требования:\nGo, PostgreSQL\n\nОбязанности:\nРазрабатывать API" {
		t.Fatalf("content=%q", content)
	}
	if got := items[0].Company.Name; got != "" {
		t.Fatalf("unexpected company=%q", got)
	}
}

func TestFetchTrudvsemRejectsBadProviderStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"500","results":{"vacancies":[]}}`))
	}))
	defer server.Close()

	if _, err := FetchTrudvsem(context.Background(), server.Client(), server.URL, "аналитик", "", 1); err == nil {
		t.Fatal("expected provider status error")
	}
}
