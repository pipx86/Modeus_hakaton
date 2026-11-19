package api

import (
	"math"
	"net/http"

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
