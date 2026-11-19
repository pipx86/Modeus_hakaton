package api

import (
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
)

type correlationMetric struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type correlationResponse struct {
	SampleSize int                 `json:"sample_size"`
	Metrics    []correlationMetric `json:"metrics"`
	Matrix     [][]float64         `json:"matrix"`
}

type skillNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Type  string `json:"type"`
	Count int    `json:"count"`
}

type skillEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Weight int    `json:"weight"`
}

type skillGraphResponse struct {
	SampleSize int         `json:"sample_size"`
	Nodes      []skillNode `json:"nodes"`
	Edges      []skillEdge `json:"edges"`
}

type runFeatures struct {
	duration  float64
	roles     float64
	functions float64
	tasks     float64
	actions   float64
	evidence  float64
}

var correlationMetrics = []correlationMetric{
	{Key: "duration", Label: "Длительность, мин"},
	{Key: "roles", Label: "Роли"},
	{Key: "functions", Label: "Функции"},
	{Key: "tasks", Label: "Задачи"},
	{Key: "actions", Label: "Действия"},
	{Key: "evidence", Label: "Цитаты"},
}

func featuresOf(run *domain.Run, nodes []domain.Node) runFeatures {
	features := runFeatures{}
	if run.StartedAt != nil && run.FinishedAt != nil {
		features.duration = run.FinishedAt.Sub(*run.StartedAt).Minutes()
	}
	for _, node := range nodes {
		switch node.Type {
		case domain.NodeRole:
			features.roles++
		case domain.NodeFunction:
			features.functions++
		case domain.NodeTask:
			features.tasks++
		case domain.NodeAction:
			features.actions++
		}
		features.evidence += float64(len(node.Evidence))
	}
	return features
}

func featureColumns(features []runFeatures) [][]float64 {
	columns := make([][]float64, len(correlationMetrics))
	for index := range columns {
		columns[index] = make([]float64, len(features))
	}
	for row, item := range features {
		columns[0][row] = item.duration
		columns[1][row] = item.roles
		columns[2][row] = item.functions
		columns[3][row] = item.tasks
		columns[4][row] = item.actions
		columns[5][row] = item.evidence
	}
	return columns
}

func pearson(left, right []float64) float64 {
	if len(left) != len(right) || len(left) < 2 {
		return 0
	}
	var sumLeft, sumRight float64
	for index := range left {
		sumLeft += left[index]
		sumRight += right[index]
	}
	meanLeft := sumLeft / float64(len(left))
	meanRight := sumRight / float64(len(right))
	var covariance, varianceLeft, varianceRight float64
	for index := range left {
		dl := left[index] - meanLeft
		dr := right[index] - meanRight
		covariance += dl * dr
		varianceLeft += dl * dl
		varianceRight += dr * dr
	}
	if varianceLeft == 0 || varianceRight == 0 {
		return 0
	}
	value := covariance / math.Sqrt(varianceLeft*varianceRight)
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return math.Round(value*100) / 100
}

// handleDashboardCorrelation godoc
// @Summary      Correlation matrix for completed runs
// @Description  Deterministic Pearson correlation between run duration, tree structure and evidence volume across completed runs. Admin and user.
// @Tags         dashboard
// @Produce      json
// @Success      200  {object}  correlationResponse
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/dashboard/correlation [get]
func (s *Server) handleDashboardCorrelation(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser) {
		return
	}
	runs, err := s.store.ListRuns()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось загрузить исследования")
		return
	}
	features := make([]runFeatures, 0, len(runs))
	for _, run := range runs {
		if run == nil || run.Status != domain.RunDone || run.StartedAt == nil || run.FinishedAt == nil {
			continue
		}
		nodes, err := s.store.GetTree(run.ID)
		if err != nil {
			continue
		}
		features = append(features, featuresOf(run, nodes))
	}
	response := correlationResponse{
		SampleSize: len(features),
		Metrics:    correlationMetrics,
		Matrix:     make([][]float64, len(correlationMetrics)),
	}
	columns := featureColumns(features)
	for row := range response.Matrix {
		response.Matrix[row] = make([]float64, len(correlationMetrics))
		for column := range response.Matrix[row] {
			response.Matrix[row][column] = pearson(columns[row], columns[column])
		}
		// A metric is always perfectly correlated with itself, including the
		// degenerate case where all runs share the same value.
		response.Matrix[row][row] = 1
	}
	writeJSON(w, http.StatusOK, response)
}

// handleDashboardSkills godoc
// @Summary      Co-occurrence graph of tree competencies
// @Description  Deterministic graph of function/task/action titles that appear together in completed run trees. Admin and user.
// @Tags         dashboard
// @Produce      json
// @Success      200  {object}  skillGraphResponse
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/dashboard/skills [get]
func (s *Server) handleDashboardSkills(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser) {
		return
	}
	runs, err := s.store.ListRuns()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось загрузить исследования")
		return
	}
	counts := map[string]int{}
	types := map[string]string{}
	edgeCounts := map[[2]string]int{}
	sampleSize := 0
	for _, run := range runs {
		if run == nil || run.Status != domain.RunDone {
			continue
		}
		nodes, err := s.store.GetTree(run.ID)
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		labels := make([]string, 0, len(nodes))
		for _, node := range nodes {
			if node.Type != domain.NodeFunction && node.Type != domain.NodeTask && node.Type != domain.NodeAction {
				continue
			}
			label := strings.TrimSpace(node.Title)
			if label == "" {
				continue
			}
			key := strings.ToLower(label)
			if seen[key] {
				continue
			}
			seen[key] = true
			counts[key]++
			types[key] = string(node.Type)
			labels = append(labels, key)
		}
		sampleSize++
		for left := 0; left < len(labels); left++ {
			for right := left + 1; right < len(labels); right++ {
				first, second := labels[left], labels[right]
				if first > second {
					first, second = second, first
				}
				edgeCounts[[2]string{first, second}]++
			}
		}
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		if counts[keys[left]] != counts[keys[right]] {
			return counts[keys[left]] > counts[keys[right]]
		}
		return keys[left] < keys[right]
	})
	if len(keys) > 12 {
		keys = keys[:12]
	}
	nodeIDs := map[string]string{}
	nodes := make([]skillNode, 0, len(keys))
	for index, key := range keys {
		id := "n" + strconv.Itoa(index+1)
		nodeIDs[key] = id
		label := strings.TrimSpace(key)
		nodes = append(nodes, skillNode{ID: id, Label: label, Type: types[key], Count: counts[key]})
	}
	edges := make([]skillEdge, 0, len(edgeCounts))
	for key, weight := range edgeCounts {
		source, sourceOK := nodeIDs[key[0]]
		target, targetOK := nodeIDs[key[1]]
		if !sourceOK || !targetOK {
			continue
		}
		edges = append(edges, skillEdge{Source: source, Target: target, Weight: weight})
	}
	sort.Slice(edges, func(left, right int) bool {
		if edges[left].Weight != edges[right].Weight {
			return edges[left].Weight > edges[right].Weight
		}
		if edges[left].Source != edges[right].Source {
			return edges[left].Source < edges[right].Source
		}
		return edges[left].Target < edges[right].Target
	})
	if len(edges) > 30 {
		edges = edges[:30]
	}
	writeJSON(w, http.StatusOK, skillGraphResponse{SampleSize: sampleSize, Nodes: nodes, Edges: edges})
}
