package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/store/db"

	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Server) academicQueries(w http.ResponseWriter, r *http.Request, roles ...auth.Role) (*db.Queries, bool) {
	if !auth.RequireRole(w, r, roles...) {
		return nil, false
	}
	pg, ok := s.store.(*PGStore)
	if !ok || pg.Pool == nil {
		writeErr(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "учебный каталог требует PostgreSQL")
		return nil, false
	}
	return db.New(pg.Pool), true
}

func academicPage(w http.ResponseWriter, values url.Values) (limit, offset int32, ok bool) {
	limit, err := queryInt(values, "limit", 50, 1, 200)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return 0, 0, false
	}
	offset, err = queryInt(values, "offset", 0, 0, 2147483647)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return 0, 0, false
	}
	return limit, offset, true
}

func academicUUID(w http.ResponseWriter, value, field string, optional bool) (pgtype.UUID, bool) {
	var id pgtype.UUID
	if optional && value == "" {
		return id, true
	}
	if err := id.Scan(value); err != nil || !id.Valid {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", field+" должен быть UUID")
		return id, false
	}
	return id, true
}

func (s *Server) handleAcademicPrograms(w http.ResponseWriter, r *http.Request) {
	queries, ok := s.academicQueries(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleStudent)
	if !ok {
		return
	}
	limit, offset, ok := academicPage(w, r.URL.Query())
	if !ok {
		return
	}
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	if len(search) > 256 {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "слишком длинная строка поиска")
		return
	}
	page, err := queries.ListAcademicPrograms(r.Context(), db.ListAcademicProgramsParams{
		Search: search, PageSize: limit, PageOffset: offset,
	})
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Items  json.RawMessage `json:"items"`
		Total  int64           `json:"total"`
		Limit  int32           `json:"limit"`
		Offset int32           `json:"offset"`
	}{Items: page.Items, Total: page.Total, Limit: limit, Offset: offset})
}

func (s *Server) handleAcademicProgram(w http.ResponseWriter, r *http.Request) {
	queries, ok := s.academicQueries(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleStudent)
	if !ok {
		return
	}
	id, ok := academicUUID(w, r.PathValue("id"), "program_id", false)
	if !ok {
		return
	}
	item, err := queries.GetAcademicProgram(r.Context(), id)
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(item))
}

func (s *Server) handleAcademicSummary(w http.ResponseWriter, r *http.Request) {
	s.writeAcademicGap(w, r, r.PathValue("id"), r.URL.Query().Get("taxonomy_id"))
}

func (s *Server) handleLocalAcademicGap(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP) {
		return
	}
	var request struct {
		ProgramID  string `json:"program_id"`
		TaxonomyID string `json:"taxonomy_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || decoder.Decode(new(any)) != io.EOF {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "нужен один JSON-объект с program_id и taxonomy_id")
		return
	}
	s.writeAcademicGap(w, r, request.ProgramID, request.TaxonomyID)
}

func (s *Server) writeAcademicGap(w http.ResponseWriter, r *http.Request, program, taxonomy string) {
	queries, ok := s.academicQueries(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleStudent)
	if !ok {
		return
	}
	programID, ok := academicUUID(w, program, "program_id", false)
	if !ok {
		return
	}
	taxonomyID, ok := academicUUID(w, taxonomy, "taxonomy_id", true)
	if !ok {
		return
	}
	report, err := queries.GetAcademicTaxonomyGap(r.Context(), db.GetAcademicTaxonomyGapParams{
		ProgramID: programID, TaxonomyID: taxonomyID,
	})
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(report))
}

func (s *Server) handleLocalTaxonomies(w http.ResponseWriter, r *http.Request) {
	queries, ok := s.academicQueries(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleStudent)
	if !ok {
		return
	}
	limit, offset, ok := academicPage(w, r.URL.Query())
	if !ok {
		return
	}
	items, err := queries.ListLocalTaxonomies(r.Context(), db.ListLocalTaxonomiesParams{PageSize: limit, PageOffset: offset})
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	if items == nil {
		items = []db.ListLocalTaxonomiesRow{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleLocalTaxonomy(w http.ResponseWriter, r *http.Request) {
	queries, ok := s.academicQueries(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleStudent)
	if !ok {
		return
	}
	id, ok := academicUUID(w, r.PathValue("id"), "taxonomy_id", false)
	if !ok {
		return
	}
	taxonomy, err := queries.GetLocalTaxonomy(r.Context(), id)
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(taxonomy))
}
