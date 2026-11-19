package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"activity-descriptor/internal/domain"
)

func TestDashboardCorrelation(t *testing.T) {
	store := newMemStore()
	start := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	for index, nodes := range [][]domain.Node{
		{
			{ID: "r1", Type: domain.NodeRole, Title: "Роль", Evidence: []domain.Evidence{{ID: "e1"}, {ID: "e2"}}},
			{ID: "f1", Type: domain.NodeFunction, Title: "Функция"},
		},
		{
			{ID: "r2", Type: domain.NodeRole, Title: "Роль", Evidence: []domain.Evidence{{ID: "e3"}}},
			{ID: "f2", Type: domain.NodeFunction, Title: "Функция"},
			{ID: "t2", Type: domain.NodeTask, Title: "Задача"},
		},
		{
			{ID: "r3", Type: domain.NodeRole, Title: "Роль"},
			{ID: "f3", Type: domain.NodeFunction, Title: "Функция"},
			{ID: "t3", Type: domain.NodeTask, Title: "Задача"},
			{ID: "a3", Type: domain.NodeAction, Title: "Действие"},
		},
	} {
		runID := "run-" + string(rune('1'+index))
		finished := start.Add(time.Duration(index+1) * time.Hour)
		store.seedRun(domain.Run{
			ID:         runID,
			Status:     domain.RunDone,
			Profession: "cook",
			StartedAt:  &start,
			FinishedAt: &finished,
		})
		store.seedNodes(runID, nodes...)
	}

	handler := NewServer(store).Routes()
	recorder := do(t, handler, http.MethodGet, "/api/v1/dashboard/correlation", "user", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var body correlationResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.SampleSize != 3 {
		t.Fatalf("sample_size = %d, want 3", body.SampleSize)
	}
	if len(body.Matrix) != len(correlationMetrics) {
		t.Fatalf("matrix rows = %d, want %d", len(body.Matrix), len(correlationMetrics))
	}
	for row := range body.Matrix {
		if len(body.Matrix[row]) != len(correlationMetrics) {
			t.Fatalf("matrix row %d has %d columns", row, len(body.Matrix[row]))
		}
		if body.Matrix[row][row] != 1 {
			t.Fatalf("diagonal[%d] = %v, want 1", row, body.Matrix[row][row])
		}
		for column := range body.Matrix[row] {
			if body.Matrix[row][column] != body.Matrix[column][row] {
				t.Fatalf("matrix is not symmetric at %d,%d", row, column)
			}
		}
	}

	unauthorized := do(t, handler, http.MethodGet, "/api/v1/dashboard/correlation", "", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", unauthorized.Code)
	}
}

func TestDashboardCorrelationEmpty(t *testing.T) {
	handler := NewServer(newMemStore()).Routes()
	recorder := do(t, handler, http.MethodGet, "/api/v1/dashboard/correlation", "admin", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	var body correlationResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.SampleSize != 0 || len(body.Matrix) != len(correlationMetrics) {
		t.Fatalf("unexpected empty response: %+v", body)
	}
}
