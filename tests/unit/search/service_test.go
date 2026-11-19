package search_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"activity-descriptor/internal/embeddings"
	"activity-descriptor/internal/search"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v4"
)

type embeddingTransport func(*http.Request) (*http.Response, error)

func (fn embeddingTransport) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestIndexSourceChunksAndRollsBackIfTextChanges(t *testing.T) {
	source := validID(t, "805cd798-35d0-4637-ab57-00d9cd706624")
	fragment := validID(t, "805cd798-35d0-4637-ab57-00d9cd706625")
	raw := " " + strings.Repeat("ё", 800) + " !"
	runes := []rune(raw)
	expected := []string{string(runes[:800]), string(runes[700:])}
	vector := make([]float32, 768)
	vector[0] = 1
	payload, err := json.Marshal(map[string]any{"embeddings": [][]float32{vector}})
	if err != nil {
		t.Fatal(err)
	}
	vectorSQL := "[1" + strings.Repeat(",0", 767) + "]"
	for _, changed := range []bool{false, true} {
		pool, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		var chunks int
		service := search.Service{Pool: pool, Model: embeddings.Ollama{
			BaseURL: "http://model.test", Model: "test-embedding", Client: &http.Client{
				Transport: embeddingTransport(func(req *http.Request) (*http.Response, error) {
					var call struct {
						Input string `json:"input"`
						Model string `json:"model"`
					}
					err := json.NewDecoder(req.Body).Decode(&call)
					if err != nil || req.URL.Path != "/api/embed" || call.Model != "test-embedding" ||
						chunks >= len(expected) || call.Input != expected[chunks] {
						t.Fatalf("запрос векторизации: %#v, %v", call, err)
					}
					chunks++
					return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(payload))}, nil
				}),
			},
		}}
		pool.ExpectQuery("FROM sources WHERE id").WithArgs(source).
			WillReturnRows(pgxmock.NewRows([]string{"content"}).AddRow(raw))
		pool.ExpectBegin()
		current := raw
		if changed {
			current += " обновлено"
		}
		pool.ExpectQuery("FOR UPDATE").WithArgs(source).WillReturnRows(pgxmock.NewRows([]string{"content"}).AddRow(current))
		if changed {
			pool.ExpectRollback()
		} else {
			pool.ExpectExec("DELETE FROM source_fragments").WithArgs(source).WillReturnResult(pgxmock.NewResult("DELETE", 1))
			for i, quote := range expected {
				pool.ExpectQuery("INSERT INTO source_fragments").WithArgs(source, i, quote).
					WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(fragment))
				pool.ExpectExec("INSERT INTO fragment_embeddings").WithArgs(fragment, service.Model.Model, vectorSQL).
					WillReturnResult(pgxmock.NewResult("INSERT", 1))
			}
			pool.ExpectCommit()
		}
		err = service.IndexSource(context.Background(), source)
		if chunks != len(expected) || (changed && (err == nil || !strings.Contains(err.Error(), "источник изменился"))) || (!changed && err != nil) {
			t.Fatalf("индексация после изменения=%v: фрагментов %d, ошибка %v", changed, chunks, err)
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIndexSourceRejectsMissingContentAndPreservesReadError(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	service := search.Service{Pool: pool}
	ctx := context.Background()
	source := validID(t, "805cd798-35d0-4637-ab57-00d9cd706624")
	if err := service.IndexSource(ctx, pgtype.UUID{}); err == nil {
		t.Fatal("отсутствующий идентификатор принят")
	}
	readErr := errors.New("не получилось прочитать источник")
	pool.ExpectQuery("FROM sources").WithArgs(source).WillReturnError(readErr)
	if err := service.IndexSource(ctx, source); !errors.Is(err, readErr) {
		t.Fatalf("ошибка PostgreSQL: %v", err)
	}
	pool.ExpectQuery("FROM sources").WithArgs(source).WillReturnRows(pgxmock.NewRows([]string{"content"}).AddRow(" \n  "))
	if err := service.IndexSource(ctx, source); err == nil {
		t.Fatal("пустой текст принят")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func validID(t *testing.T, raw string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(raw); err != nil {
		t.Fatal(err)
	}
	return id
}
