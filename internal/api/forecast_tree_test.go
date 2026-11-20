package api

import (
	"testing"

	"activity-descriptor/internal/domain"
)

func TestBuildTreeForecastScenario(t *testing.T) {
	nodes := []domain.Node{
		{ID: "n1", Type: domain.NodeTask, Title: "Ручная проверка отчётов"},
		{ID: "n2", Type: domain.NodeTask, Title: "Автоматизация данных"},
	}
	result := buildTreeForecast("Аналитик", "run-1", nodes, []skillTrendPoint{
		{Skill: "Автоматизация данных", Change: 20, Confidence: 0.8},
	}, false)
	if result.Scenario || len(result.Nodes) != 2 {
		t.Fatalf("result = %+v", result)
	}
	for _, node := range result.Nodes {
		if node.ID == "n2" && node.Status != "grow" {
			t.Fatalf("growing node = %+v", node)
		}
		if node.ID == "n1" && node.Status != "decline" {
			t.Fatalf("manual node = %+v", node)
		}
	}
}

func TestBuildTreeForecastMarksScenarioWithoutHistory(t *testing.T) {
	result := buildTreeForecast("Аналитик", "run-1", []domain.Node{{ID: "n1", Type: domain.NodeRole, Title: "Аналитик"}}, nil, true)
	if !result.Scenario || result.Summary == "" {
		t.Fatalf("result = %+v", result)
	}
}
