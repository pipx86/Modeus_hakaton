package api

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"

	"activity-descriptor/internal/ingestion"
	"activity-descriptor/internal/store/db"
)

func (s *Server) handleObservedSkills(w http.ResponseWriter, r *http.Request) {
	queries, ok := s.vacancyQueries(w, r)
	if !ok {
		return
	}
	values := r.URL.Query()
	months, err := queryInt(values, "months", 3, 1, 12)
	if err != nil || (months != 1 && months != 3 && months != 6 && months != 12) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "months должен быть 1, 3, 6 или 12")
		return
	}
	limit, err := queryInt(values, "n", 25, 1, 50)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	direction := values.Get("direction")
	if direction == "" {
		direction = "declining"
	}
	sortBy := values.Get("sort")
	if sortBy == "" {
		sortBy = "growth"
	}
	if (direction != "declining" && direction != "growing" && direction != "all") ||
		(sortBy != "growth" && sortBy != "popular") {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "недопустимые direction или sort")
		return
	}
	minFrequency, valid := readMinFrequency(w, r, 50)
	if !valid {
		return
	}
	series, valid := readForecastSeries(w, r)
	if !valid {
		return
	}
	result, err := queries.GetObservedSkillChanges(r.Context(), db.GetObservedSkillChangesParams{
		Series: series, RequiredPoints: months + 1, MinFrequency: minFrequency,
		Direction: direction, SortBy: sortBy, ResultLimit: limit,
	})
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Series         string          `json:"series"`
		Direction      string          `json:"direction"`
		Limit          int32           `json:"n"`
		Months         int32           `json:"months"`
		RequiredPoints int32           `json:"required_points"`
		MaxPoints      int64           `json:"max_points"`
		Forecasts      json.RawMessage `json:"forecasts"`
	}{
		Series: series, Direction: direction, Limit: limit, Months: months,
		RequiredPoints: months + 1, MaxPoints: result.MaxPoints, Forecasts: result.Forecasts,
	})
}

func (s *Server) handleSkillForecast(w http.ResponseWriter, r *http.Request) {
	s.writeSkillForecast(w, r, "single")
}

func (s *Server) handleAllSkillForecasts(w http.ResponseWriter, r *http.Request) {
	s.writeSkillForecast(w, r, "all")
}

func (s *Server) handleTopSkillForecasts(w http.ResponseWriter, r *http.Request) {
	s.writeSkillForecast(w, r, "top")
}

func (s *Server) handlePopularSkillForecasts(w http.ResponseWriter, r *http.Request) {
	s.writeSkillForecast(w, r, "popular")
}

func (s *Server) writeSkillForecast(w http.ResponseWriter, r *http.Request, kind string) {
	queries, ok := s.vacancyQueries(w, r)
	if !ok {
		return
	}
	values := r.URL.Query()
	series, valid := readForecastSeries(w, r)
	if !valid {
		return
	}
	months, err := queryInt(values, "months", 12, 1, 24)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	params := db.GetSkillTrendForecastsParams{
		Series: series, Months: months, Direction: "all", SortBy: "skill",
	}
	switch kind {
	case "single":
		params.SkillName = strings.TrimSpace(r.PathValue("skill"))
		params.ResultLimit = 1
		if params.SkillName == "" || len(params.SkillName) > 256 || strings.ContainsRune(params.SkillName, 0) {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "недопустимое название навыка")
			return
		}
	case "all":
		params.ResultLimit, err = queryInt(values, "limit", 500, 1, 5000)
		if err == nil {
			params.ResultOffset, err = queryInt(values, "offset", 0, 0, 2147483647)
		}
	default:
		params.OnlyTrends = true
		params.ResultLimit, err = queryInt(values, "n", 25, 1, 50)
		params.SortBy = "popular"
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if kind == "top" {
		params.MinFrequency = 50
		params.Direction = "growing"
		params.SortBy = "growth"
		if value := values.Get("direction"); value != "" {
			params.Direction = value
		}
		if value := values.Get("sort"); value != "" {
			params.SortBy = value
		}
		if (params.Direction != "growing" && params.Direction != "declining") ||
			(params.SortBy != "growth" && params.SortBy != "popular") {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "недопустимые direction или sort")
			return
		}
	}
	params.MinFrequency, valid = readMinFrequency(w, r, params.MinFrequency)
	if !valid {
		return
	}
	result, err := queries.GetSkillTrendForecasts(r.Context(), params)
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	if kind == "single" {
		var items []json.RawMessage
		if err = json.Unmarshal(result.Forecasts, &items); err != nil {
			sourceEvidenceError(w, translate(err))
			return
		}
		if len(items) == 0 {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "навык отсутствует в снимках серии")
			return
		}
		writeJSON(w, http.StatusOK, items[0])
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"series": series, "method": "trend", "direction": params.Direction,
		"months": months, "limit": params.ResultLimit, "offset": params.ResultOffset,
		"total": result.Total, "forecasts": json.RawMessage(result.Forecasts),
	})
}

func (s *Server) handleSkillForecastStatus(w http.ResponseWriter, r *http.Request) {
	queries, ok := s.vacancyQueries(w, r)
	if !ok {
		return
	}
	series, ok := readForecastSeries(w, r)
	if !ok {
		return
	}
	status, err := queries.GetSkillForecastStatus(r.Context(), series)
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"series": series, "engine": "trend", "snapshots": status.Snapshots, "ready": status.Snapshots >= 2,
		"first_month": status.FirstMonth, "last_month": status.LastMonth,
	})
}

func readForecastSeries(w http.ResponseWriter, r *http.Request) (string, bool) {
	series := strings.TrimSpace(r.URL.Query().Get("series"))
	if series == "" {
		series = ingestion.MarketSkillSeries
	}
	if len(series) > 267 || strings.ContainsRune(series, 0) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "недопустимая серия снимков")
		return "", false
	}
	return series, true
}

func readMinFrequency(w http.ResponseWriter, r *http.Request, fallback float64) (float64, bool) {
	raw := r.URL.Query().Get("min_freq")
	if raw == "" {
		return fallback, true
	}
	frequency, err := strconv.ParseFloat(raw, 64)
	if err != nil || frequency < 0 || math.IsInf(frequency, 0) || math.IsNaN(frequency) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "min_freq должен быть конечным неотрицательным числом")
		return 0, false
	}
	return frequency, true
}
