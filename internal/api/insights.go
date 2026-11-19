package api

// Consolidated mentor-insights read API.
//
// The lead registers these two routes in Server.Routes:
//
//	GET /api/v1/runs/{id}/insights
//	GET /api/v1/runs/{id}/coverage?format=csv
//
// Both endpoints are read-only and share the RBAC of the other tree reads
// (admin, teacher, rop, guest; student is intentionally excluded). They need
// PostgreSQL because run sources live in the run_sources table: the in-memory
// store returns 501 NOT_IMPLEMENTED exactly like the other source-backed read
// endpoints. Invalid or unknown run ids return 404.
//
// GET /insights is the single aggregate behind features #1 (provenance score),
// #2 ("why this node"), #3 (tree health score), #5 (red flags), #8 (market
// heat) and #9 (coverage matrix). It returns all of them in one payload so the
// frontend does not issue six requests:
//
//	{
//	  "run_id": "...",
//	  "provenance": {"nodes": {"<node_id>": {"source_count": 2,
//	     "source_types": ["interview","standard"], "evidence_count": 3}}},
//	  "health": {"score": 0, "breakdown": {"groundedness":0,"completeness":0,
//	     "duplicates":0,"orphans":0}, "weights": {...}, "counts": {...},
//	     "formula": "..."},
//	  "flags": [{"node_id":"...","codes":["NO_EVIDENCE"],"reasons":["..."]}],
//	  "market_heat": {"source":"run.skill_freq|vacancy_sources|none",
//	     "sample_size": 2,
//	     "top":[{"skill":"SQL","frequency":42}],
//	     "nodes":{"<node_id>":{"score":42,"matches":["SQL"]}}},
//	  "coverage": {"columns":["interview","standard","vacancy","document"],
//	     "rows":[{"node_id":"...","title":"...","type":"function",
//	     "cells":{"interview":2,"standard":0,"vacancy":0,"document":0}}]},
//	  "sources": [{"id":"...","type":"...","title":"...","priority":300}],
//	  "node_explanations": {"<node_id>": {"summary":"...","evidence":[
//	     {"quote":"...","source_id":"...","source_type":"...","priority":300}]}}
//	}
//
// All values are deterministic: no LLM calls and no external calls happen
// while building the payload (see internal/analysis/insights.go).
//
// GET /coverage returns the same coverage matrix as JSON by default and as
// text/csv (header node_id,title,type,interview,standard,vacancy,document) when
// the `format=csv` query parameter is present.

import (
	"bytes"
	"context"
	"net/http"
	"strings"

	"activity-descriptor/internal/analysis"
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/logging"
)

// insightsReader is the narrow read surface required by the insights
// endpoints. *PGStore implements all three methods; unit tests substitute an
// in-memory fake.
type insightsReader interface {
	GetRun(id string) (*domain.Run, error)
	GetTree(runID string) ([]domain.Node, error)
	ListRunSources(ctx context.Context, runID string) ([]domain.Source, error)
}

var _ insightsReader = (*PGStore)(nil)

// coverageResponse is the JSON body of GET /runs/{id}/coverage. The embedded
// rows are exactly the rows of the insights coverage matrix, so the standalone
// endpoint and the aggregate stay in sync.
type coverageResponse struct {
	RunID   string                 `json:"run_id"`
	Columns []string               `json:"columns"`
	Rows    []analysis.CoverageRow `json:"rows"`
}

// insightsHTTPError maps a store error to the same HTTP codes as the other
// run read endpoints, falling back to a generic 500.
func insightsHTTPError(w http.ResponseWriter, err error, message string) {
	if mapStoreError(w, err) {
		return
	}
	writeErr(w, http.StatusInternalServerError, "INTERNAL", message)
}

// requireInsightsReader resolves the run-source-capable reader or writes the
// 501 response for stores that cannot serve insights (in-memory Store, or a
// PGStore without an initialized pool).
func (s *Server) requireInsightsReader(w http.ResponseWriter) (insightsReader, bool) {
	if pg, ok := s.store.(*PGStore); ok {
		if pg == nil || pg.Pool == nil {
			writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "для инсайтов требуется PostgreSQL")
			return nil, false
		}
		return pg, true
	}
	if reader, ok := s.store.(insightsReader); ok {
		return reader, true
	}
	writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "для инсайтов требуется PostgreSQL")
	return nil, false
}

// loadRunInsights reads the run, its tree and its linked sources, then builds
// the deterministic insights payload.
//
// Market heat uses run.SkillFreq when it is present. Otherwise, when the store
// can serve source contents (PGStore.ListRunSourceContents), the deterministic
// vacancy/standard-content fallback is computed; that fallback is best-effort:
// read errors are logged and never fail the request.
func (s *Server) loadRunInsights(ctx context.Context, reader insightsReader, runID string) (analysis.Insights, error) {
	run, err := reader.GetRun(runID)
	if err != nil {
		return analysis.Insights{}, err
	}
	nodes, err := reader.GetTree(runID)
	if err != nil {
		return analysis.Insights{}, err
	}
	sources, err := reader.ListRunSources(ctx, runID)
	if err != nil {
		return analysis.Insights{}, err
	}
	market := analysis.MarketInput{SkillFreq: run.SkillFreq}
	if !insightHasUsableSkillFreq(run.SkillFreq) {
		if contentReader, ok := reader.(runSourceContentReader); ok {
			contents, contentErr := contentReader.ListRunSourceContents(ctx, runID)
			if contentErr != nil {
				logging.L().Warn("run_insights_source_contents_failed", "run_id", runID, "error", contentErr)
			} else if fallbackFreq, sampleSize := analysis.MarketSkillFrequencyFromSources(contents); sampleSize > 0 && len(fallbackFreq) > 0 {
				market.FallbackFreq = fallbackFreq
				market.FallbackSampleSize = sampleSize
			}
		}
	}
	insights := analysis.BuildInsightsWithMarket(nodes, sources, market)
	insights.RunID = run.ID
	return insights, nil
}

// insightHasUsableSkillFreq reports whether the run skill-frequency map contains
// at least one positive frequency; only then does it take precedence over the
// deterministic source-content fallback.
func insightHasUsableSkillFreq(skillFreq map[string]int) bool {
	for skill, frequency := range skillFreq {
		if frequency > 0 && strings.TrimSpace(skill) != "" {
			return true
		}
	}
	return false
}

// handleRunInsights godoc
// @Summary      Consolidated tree insights for a run
// @Description  Returns provenance score per node, tree health score with a groundedness/completeness/duplicates/orphans breakdown, five red-flag rules with reasons, market-heat overlay, the source-type coverage matrix, run sources and template-only "why this node" explanations. Market heat prefers Run.SkillFreq (source "run.skill_freq") and otherwise deterministically extracts skill frequencies from the content of vacancy/standard run sources (source "vacancy_sources" with sample_size), empty with source "none" when no market data exists. Deterministic, no LLM calls.
// @Tags         runs
// @Produce      json
// @Param        id   path      string  true  "Run ID"
// @Success      200  {object}  analysis.Insights
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/insights [get]
func (s *Server) handleRunInsights(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser) {
		return
	}
	reader, ok := s.requireInsightsReader(w)
	if !ok {
		return
	}
	insights, err := s.loadRunInsights(r.Context(), reader, r.PathValue("id"))
	if err != nil {
		insightsHTTPError(w, err, "не удалось построить инсайты дерева")
		return
	}
	writeJSON(w, http.StatusOK, insights)
}

// handleRunCoverage godoc
// @Summary      Source-type coverage matrix for a run
// @Description  Returns role/function/task rows with evidence counts per source type (interview, standard, vacancy, document; every cell is always present). `format=csv` returns the same matrix as text/csv with header node_id,title,type,interview,standard,vacancy,document; any other format returns JSON.
// @Tags         runs
// @Produce      json
// @Produce      text/csv
// @Param        id      path   string  true   "Run ID"
// @Param        format  query  string  false  "csv for text/csv, anything else (default) for JSON"
// @Success      200  {object}  coverageResponse
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/coverage [get]
func (s *Server) handleRunCoverage(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser) {
		return
	}
	reader, ok := s.requireInsightsReader(w)
	if !ok {
		return
	}
	runID := r.PathValue("id")
	run, err := reader.GetRun(runID)
	if err != nil {
		insightsHTTPError(w, err, "не удалось загрузить исследование для матрицы покрытия")
		return
	}
	nodes, err := reader.GetTree(runID)
	if err != nil {
		insightsHTTPError(w, err, "не удалось загрузить дерево для матрицы покрытия")
		return
	}
	sources, err := reader.ListRunSources(r.Context(), runID)
	if err != nil {
		insightsHTTPError(w, err, "не удалось загрузить источники для матрицы покрытия")
		return
	}
	coverage := analysis.BuildCoverageMatrix(nodes, sources)
	if isCSVFormat(r.URL.Query().Get("format")) {
		writeCoverageCSV(w, run.ID, coverage)
		return
	}
	writeJSON(w, http.StatusOK, coverageResponse{
		RunID:   run.ID,
		Columns: coverage.Columns,
		Rows:    coverage.Rows,
	})
}

// isCSVFormat reports whether the format query parameter selects CSV. The
// comparison is case-insensitive and tolerates surrounding spaces; every other
// value (including an empty one) selects the default JSON response.
func isCSVFormat(format string) bool {
	return strings.EqualFold(strings.TrimSpace(format), "csv")
}

// writeCoverageCSV renders the matrix into a buffer first so a writer error
// cannot leave a half-written CSV with a 200 status.
func writeCoverageCSV(w http.ResponseWriter, runID string, coverage analysis.Coverage) {
	var buffer bytes.Buffer
	if err := analysis.WriteCoverageCSV(&buffer, coverage); err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось сформировать CSV матрицы покрытия")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="run-`+insightFilenameID(runID)+`-coverage.csv"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buffer.Bytes())
}

// insightFilenameID keeps only characters that are safe inside a quoted
// Content-Disposition filename. Run ids are UUIDs, so this is a no-op for
// real data while keeping test/fixture ids safe.
func insightFilenameID(runID string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(runID)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			builder.WriteRune(r)
		}
	}
	if builder.Len() == 0 {
		return "unknown"
	}
	return builder.String()
}
