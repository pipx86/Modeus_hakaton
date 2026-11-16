package ingestion

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestФорматыВакансий(t *testing.T) {
	for _, tc := range []struct{ format, data string }{
		{"json", `[{"id":"123","name":"Разработчик"}]`},
		{"json", `{"items":[{"id":"123","name":"Разработчик"}]}`},
		{"csv", "id,name,description\n123,Разработчик,Описание\n"},
	} {
		items, err := ReadVacancies(strings.NewReader(tc.data), tc.format)
		if err != nil || len(items) != 1 || items[0].ID != "123" {
			t.Fatalf("%s: %v %v", tc.format, items, err)
		}
	}
	if _, err := ReadVacancies(strings.NewReader("id,name\n1,\n"), "csv"); err == nil {
		t.Fatal("пустое название принято")
	}
}

func TestСтраницыHH(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("HH-User-Agent") != "test contact" {
			t.Error("нет заголовка")
		}
		page := r.URL.Query().Get("page")
		fmt.Fprintf(w, `{"pages":2,"items":[{"id":"%s","name":"Вакансия"}]}`, page)
	}))
	defer server.Close()
	items, err := FetchHH(context.Background(), server.Client(), server.URL, "аналитик", "test contact", "", 3)
	if err != nil || len(items) != 2 {
		t.Fatalf("%v %v", items, err)
	}
}

func TestПовторныйИмпортВакансий(t *testing.T) {
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("для теста нужна INGEST_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	id := fmt.Sprintf("test-%d", time.Now().UnixNano())
	defer pool.Exec(ctx, "DELETE FROM sources WHERE provider='hh' AND type='vacancy' AND external_id=$1", id)
	items := []Vacancy{{ID: id, Name: "Разработчик"}}
	first, err := SaveVacancies(ctx, pool, items)
	if err != nil || first.Inserted != 1 {
		t.Fatalf("%+v %v", first, err)
	}
	second, err := SaveVacancies(ctx, pool, items)
	if err != nil || second.Inserted != 0 || second.Skipped != 1 {
		t.Fatalf("%+v %v", second, err)
	}
	items[0].Name = "Инженер"
	third, err := SaveVacancies(ctx, pool, items)
	if err != nil || third.Updated != 1 {
		t.Fatalf("%+v %v", third, err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM sources WHERE provider='hh' AND type='vacancy' AND external_id=$1", id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("%d %v", count, err)
	}
}
