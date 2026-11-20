package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/ingestion"
	"activity-descriptor/internal/store/db"
)

type treeForecastNode struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Type       string  `json:"type"`
	Status     string  `json:"status"`
	Confidence int     `json:"confidence"`
	Reason     string  `json:"reason"`
	Trend      float64 `json:"trend_pct,omitempty"`
}

type treeForecastNewNode struct {
	Title      string `json:"title"`
	Type       string `json:"type"`
	Reason     string `json:"reason"`
	Confidence int    `json:"confidence"`
}

type treeForecastResponse struct {
	Profession    string                `json:"profession"`
	RunID         string                `json:"run_id,omitempty"`
	HorizonMonths int                   `json:"horizon_months"`
	Scenario      bool                  `json:"scenario"`
	GeneratedAt   string                `json:"generated_at"`
	Nodes         []treeForecastNode    `json:"nodes"`
	NewNodes      []treeForecastNewNode `json:"new_nodes"`
	Summary       string                `json:"summary"`
}

type skillTrendPoint struct {
	Skill      string
	Change     float64
	Confidence float64
}

// handleTreeForecast builds a scenario for the latest completed tree of a
// profession. If there is not enough historical skill data, it returns an
// explicitly marked deterministic scenario instead of pretending it is a
// statistical forecast.
func (s *Server) handleTreeForecast(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleSpecialist, auth.RoleUser) {
		return
	}
	pgStore, ok := s.store.(*PGStore)
	if !ok || pgStore.Pool == nil {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "прогноз дерева требует PostgreSQL")
		return
	}
	profession := strings.TrimSpace(r.URL.Query().Get("profession"))
	if profession == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "укажите профессию")
		return
	}
	runs, err := s.store.ListRuns()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось загрузить исследования")
		return
	}
	var run *domain.Run
	var nodes []domain.Node
	for _, candidate := range runs {
		if candidate == nil || candidate.Status != domain.RunDone {
			continue
		}
		if !(strings.EqualFold(strings.TrimSpace(candidate.Profession), profession) ||
			strings.Contains(strings.ToLower(candidate.Profession), strings.ToLower(profession)) ||
			strings.Contains(strings.ToLower(profession), strings.ToLower(candidate.Profession))) {
			continue
		}
		tree, treeErr := s.store.GetTree(candidate.ID)
		if treeErr != nil || len(tree) == 0 {
			continue
		}
		run = candidate
		nodes = tree
		break
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "завершённое исследование с деревом по профессии не найдено")
		return
	}
	trends, trendErr := loadSkillTrends(r.Context(), pgStore, 12)
	scenario := trendErr != nil || len(trends) == 0
	result := buildTreeForecast(profession, run.ID, nodes, trends, scenario)
	writeJSON(w, http.StatusOK, result)
}

func loadSkillTrends(ctx context.Context, store *PGStore, months int32) ([]skillTrendPoint, error) {
	rows, err := db.New(store.Pool).GetSkillTrendForecasts(ctx, db.GetSkillTrendForecastsParams{
		Series: ingestion.MarketSkillSeries, Months: months, Direction: "all", SortBy: "popular", ResultLimit: 50,
	})
	if err != nil {
		return nil, err
	}
	var raw []map[string]any
	if err := json.Unmarshal(rows.Forecasts, &raw); err != nil {
		return nil, err
	}
	out := make([]skillTrendPoint, 0, len(raw))
	for _, item := range raw {
		skill := strings.TrimSpace(fmt.Sprint(item["skill"]))
		if skill == "" {
			continue
		}
		out = append(out, skillTrendPoint{
			Skill:      skill,
			Change:     numberValue(item["predicted_change_pct"]),
			Confidence: numberValue(item["confidence"]),
		})
	}
	return out, nil
}

func buildTreeForecast(profession, runID string, nodes []domain.Node, trends []skillTrendPoint, scenario bool) treeForecastResponse {
	trendBySkill := make(map[string]skillTrendPoint, len(trends))
	for _, trend := range trends {
		trendBySkill[strings.ToLower(trend.Skill)] = trend
	}
	result := treeForecastResponse{
		Profession: profession, RunID: runID, HorizonMonths: 12, Scenario: scenario,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Nodes:       make([]treeForecastNode, 0, len(nodes)),
		NewNodes:    make([]treeForecastNewNode, 0),
	}
	knownTitles := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		knownTitles[strings.ToLower(node.Title)] = true
		trend, matched := trendBySkill[strings.ToLower(node.Title)]
		status := "stable"
		reason := "Для узла нет отдельного временного ряда; сохраняем позицию."
		confidence := 55
		if matched {
			switch {
			case trend.Change > 5:
				status, reason = "grow", fmt.Sprintf("Навык растёт на %.1f%% за период.", trend.Change)
			case trend.Change < -5:
				status, reason = "decline", fmt.Sprintf("Навык снижается на %.1f%% за период.", trend.Change)
			default:
				reason = "Навык стабилен."
			}
			confidence = int(50 + minFloat(45, absFloat(trend.Change)*2))
		} else if containsAny(strings.ToLower(node.Title), "ручн", "рутин", "бумаж") {
			status, reason, confidence = "decline", "Ручные операции автоматизируются быстрее остальных.", 62
		} else if containsAny(strings.ToLower(node.Title), "автоматиз", "данн", "аналит", "ии", "ai") {
			status, reason, confidence = "grow", "Направление связано с автоматизацией и работой с данными.", 66
		}
		result.Nodes = append(result.Nodes, treeForecastNode{
			ID: node.ID, Title: node.Title, Type: string(node.Type), Status: status,
			Confidence: confidence, Reason: reason, Trend: trend.Change,
		})
	}
	sort.Slice(result.Nodes, func(i, j int) bool {
		if result.Nodes[i].Status == result.Nodes[j].Status {
			return result.Nodes[i].Title < result.Nodes[j].Title
		}
		return result.Nodes[i].Status < result.Nodes[j].Status
	})
	for _, trend := range trends {
		if trend.Change <= 5 || len(result.NewNodes) >= 3 {
			continue
		}
		if knownTitles[strings.ToLower(trend.Skill)] {
			continue
		}
		result.NewNodes = append(result.NewNodes, treeForecastNewNode{
			Title: trend.Skill, Type: "action",
			Reason:     "Навык растёт в вакансиях и пока не представлен отдельным действием в дереве.",
			Confidence: int(minFloat(90, 55+trend.Change)),
		})
	}
	if scenario {
		result.Summary = "Сценарий: истории навыков пока недостаточно. Показаны направления, которые изменятся при сохранении текущих рыночных сигналов."
	} else {
		result.Summary = fmt.Sprintf("Прогноз на %d месяцев по %d узлам и %d растущим навыкам.", result.HorizonMonths, len(result.Nodes), len(result.NewNodes))
	}
	return result
}

func containsAny(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}

func numberValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	case string:
		var out float64
		_, _ = fmt.Sscan(typed, &out)
		return out
	default:
		return 0
	}
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
