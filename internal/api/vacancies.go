package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/store/db"

	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Server) vacancyQueries(w http.ResponseWriter, r *http.Request) (*db.Queries, bool) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser) {
		return nil, false
	}
	pg, ok := s.store.(*PGStore)
	if !ok || pg.Pool == nil {
		writeErr(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "каталог вакансий требует PostgreSQL")
		return nil, false
	}
	return db.New(pg.Pool), true
}

func queryInt(values url.Values, key string, fallback, min, max int32) (int32, error) {
	raw := values.Get(key)
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || parsed < int64(min) || parsed > int64(max) {
		return 0, fmt.Errorf("%s должен быть целым числом от %d до %d", key, min, max)
	}
	return int32(parsed), nil
}

func vacancyDay(values url.Values, key string) (time.Time, error) {
	raw := strings.TrimSpace(values.Get(key))
	if raw == "" {
		return time.Time{}, nil
	}
	day, err := time.ParseInLocation(time.DateOnly, raw, time.FixedZone("MSK", 3*60*60))
	if err != nil {
		return time.Time{}, fmt.Errorf("%s должен иметь формат ГГГГ-ММ-ДД", key)
	}
	return day, nil
}

func vacancyCatalogParams(values url.Values, now time.Time) (db.ListVacancyCatalogParams, error) {
	var params db.ListVacancyCatalogParams
	var err error
	params.PageSize, err = queryInt(values, "limit", 50, 1, 500)
	if err != nil {
		return params, err
	}
	params.PageOffset, err = queryInt(values, "offset", 0, 0, 2147483647)
	if err != nil {
		return params, err
	}
	months, err := queryInt(values, "months", 0, 1, 24)
	if err != nil {
		return params, err
	}
	params.Search = strings.TrimSpace(values.Get("search"))
	params.Region = strings.TrimSpace(values.Get("region"))
	params.Employer = strings.TrimSpace(values.Get("employer"))
	params.Experience = strings.ToLower(strings.TrimSpace(values.Get("experience")))
	if strings.EqualFold(params.Region, "all") {
		params.Region = ""
	}
	if len(params.Search)+len(params.Region)+len(params.Employer) > 1024 {
		return params, errors.New("слишком длинные параметры каталога")
	}
	switch params.Experience {
	case "", "junior", "middle", "senior":
	default:
		return params, errors.New("experience должен быть junior, middle или senior")
	}
	from, err := vacancyDay(values, "date_from")
	if err != nil {
		return params, err
	}
	to, err := vacancyDay(values, "date_to")
	if err != nil {
		return params, err
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		return params, errors.New("date_from должен быть не позже date_to")
	}
	if months != 0 {
		cutoff := now.UTC().Add(-time.Duration(months) * 30 * 24 * time.Hour)
		if from.Before(cutoff) {
			from = cutoff
		}
	}
	params.FromDate = pgtype.Timestamptz{Time: from, Valid: !from.IsZero()}
	if !to.IsZero() {
		params.UntilDate = pgtype.Timestamptz{Time: to.AddDate(0, 0, 1), Valid: true}
	}
	return params, nil
}

func (s *Server) handleVacancyCatalog(w http.ResponseWriter, r *http.Request) {
	queries, ok := s.vacancyQueries(w, r)
	if !ok {
		return
	}
	params, err := vacancyCatalogParams(r.URL.Query(), time.Now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	page, err := queries.ListVacancyCatalog(r.Context(), params)
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Items   json.RawMessage `json:"items"`
		Total   int64           `json:"total"`
		Limit   int32           `json:"limit"`
		Offset  int32           `json:"offset"`
		HasMore bool            `json:"has_more"`
	}{
		Items: page.Items, Total: page.Total, Limit: params.PageSize, Offset: params.PageOffset,
		HasMore: int64(params.PageOffset)+int64(params.PageSize) < page.Total,
	})
}

func (s *Server) handleVacancyDetail(w http.ResponseWriter, r *http.Request) {
	queries, ok := s.vacancyQueries(w, r)
	if !ok {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" || len(id) > 128 {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "нужен идентификатор вакансии")
		return
	}
	item, err := queries.GetVacancyCatalogEntry(r.Context(), id)
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(item))
}

func (s *Server) handleVacancyInfo(w http.ResponseWriter, r *http.Request) {
	queries, ok := s.vacancyQueries(w, r)
	if !ok {
		return
	}
	info, err := queries.GetVacancyCatalogInfo(r.Context())
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	type dates struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	var dateRange *dates
	if info.FirstPublished.Valid && info.LastPublished.Valid {
		zone := time.FixedZone("MSK", 3*60*60)
		dateRange = &dates{From: info.FirstPublished.Time.In(zone).Format(time.DateOnly), To: info.LastPublished.Time.In(zone).Format(time.DateOnly)}
	}
	writeJSON(w, http.StatusOK, struct {
		Count      int64  `json:"count"`
		WithSkills int64  `json:"with_skills"`
		DateRange  *dates `json:"date_range"`
	}{Count: info.Total, WithSkills: info.WithSkills, DateRange: dateRange})
}

func (s *Server) handleVacancyStats(w http.ResponseWriter, r *http.Request) {
	queries, ok := s.vacancyQueries(w, r)
	if !ok {
		return
	}
	stats, err := queries.GetVacancyCatalogStats(r.Context())
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Total            int64           `json:"total"`
		ByExperience     json.RawMessage `json:"by_experience"`
		SalaryByCurrency json.RawMessage `json:"salary_by_currency"`
	}{Total: stats.Total, ByExperience: stats.ByExperience, SalaryByCurrency: stats.SalaryByCurrency})
}

func (s *Server) handleVacancyAnalytics(w http.ResponseWriter, r *http.Request) {
	queries, ok := s.vacancyQueries(w, r)
	if !ok {
		return
	}
	stats, err := queries.GetVacancyCatalogRankings(r.Context())
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Regions   json.RawMessage `json:"top_regions"`
		Employers json.RawMessage `json:"top_employers"`
		Skills    json.RawMessage `json:"top_skills"`
	}{Regions: stats.TopRegions, Employers: stats.TopEmployers, Skills: stats.TopSkills})
}
