package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"activity-descriptor/internal/analysis"
	"activity-descriptor/internal/domain"
)

type treeResponseBody struct {
	Nodes    []domain.Node     `json:"nodes"`
	Insights analysis.Insights `json:"insights"`
}

func TestGetRunTreeIncludesInsights(t *testing.T) {
	store := newMemStore()
	run, err := store.CreateRun("Аналитик данных")
	if err != nil {
		t.Fatal(err)
	}
	store.seedNodes(run.ID,
		domain.Node{
			ID:       "n1",
			Type:     domain.NodeRole,
			Title:    "Аналитик данных",
			Evidence: []domain.Evidence{{ID: "e1", SourceID: "s1", Quote: "Строю дашборды"}},
		},
	)
	handler := NewServer(store).Routes()
	recorder := do(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID+"/tree", "teacher", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d: %s", recorder.Code, recorder.Body.String())
	}
	var body treeResponseBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("некорректный JSON: %v", err)
	}
	if len(body.Nodes) != 1 || body.Nodes[0].ID != "n1" {
		t.Fatalf("узлы не совпадают: %+v", body.Nodes)
	}
	if body.Insights.RunID != run.ID {
		t.Fatalf("run_id инсайтов: %q", body.Insights.RunID)
	}
	if body.Insights.Health.Score < 0 || body.Insights.Health.Score > 100 {
		t.Fatalf("health вне диапазона: %d", body.Insights.Health.Score)
	}
	if _, ok := body.Insights.Provenance.Nodes["n1"]; !ok {
		t.Fatalf("в провенансе нет узла n1: %+v", body.Insights.Provenance.Nodes)
	}
	if body.Insights.Coverage.Columns == nil {
		t.Fatal("coverage должен содержать хотя бы пустые колонки")
	}
}
