package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
)

func (p *PGStore) listSources(ctx context.Context) ([]domain.Source, error) {
	rows, err := p.Pool.Query(ctx, `SELECT id::text,type,title,COALESCE(content,''),COALESCE(source_url,''),created_at FROM sources ORDER BY created_at DESC,id LIMIT 1000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Source{}
	for rows.Next() {
		var item domain.Source
		if err = rows.Scan(&item.ID, &item.Type, &item.Title, &item.Content, &item.SourceURL, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (p *PGStore) getSource(ctx context.Context, id string) (domain.Source, error) {
	var item domain.Source
	if !validUUID(id) {
		return item, ErrNotFound
	}
	err := p.Pool.QueryRow(ctx, `SELECT id::text,type,title,COALESCE(content,''),COALESCE(source_url,''),created_at FROM sources WHERE id=$1`, id).Scan(&item.ID, &item.Type, &item.Title, &item.Content, &item.SourceURL, &item.CreatedAt)
	return item, translate(err)
}
func (p *PGStore) listEvidences(ctx context.Context, nodeID, sourceID string) ([]domain.Evidence, error) {
	var nodeArg, sourceArg any
	if nodeID != "" {
		if !validUUID(nodeID) {
			return nil, &domain.TreeValidationError{Code: "INVALID_NODE_ID", Details: "ожидается UUID узла"}
		}
		nodeArg = nodeID
	}
	if sourceID != "" {
		if !validUUID(sourceID) {
			return nil, &domain.TreeValidationError{Code: "INVALID_REFERENCE", Details: "ожидается UUID источника"}
		}
		sourceArg = sourceID
	}
	rows, err := p.Pool.Query(ctx, `SELECT id::text,node_id::text,source_id::text,COALESCE(ref_id,''),quote FROM evidence WHERE ($1::uuid IS NULL OR node_id=$1) AND ($2::uuid IS NULL OR source_id=$2) ORDER BY created_at,id LIMIT 1000`, nodeArg, sourceArg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Evidence{}
	for rows.Next() {
		var item domain.Evidence
		if err = rows.Scan(&item.ID, &item.NodeID, &item.SourceID, &item.RefID, &item.Quote); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (p *PGStore) createEvidence(ctx context.Context, nodeID string, item domain.Evidence) (domain.Evidence, error) {
	if !validUUID(nodeID) || !validUUID(item.SourceID) || strings.TrimSpace(item.Quote) == "" || (item.RefID != "" && strings.TrimSpace(item.RefID) == "") {
		return domain.Evidence{}, &domain.TreeValidationError{Code: "INVALID_EVIDENCE", Details: "нужны UUID узла и источника, непустая цитата"}
	}
	err := p.Pool.QueryRow(ctx, `INSERT INTO evidence(node_id,source_id,quote,ref_id) VALUES($1,$2,$3,NULLIF($4,'')) RETURNING id::text,node_id::text,source_id::text,COALESCE(ref_id,''),quote`, nodeID, item.SourceID, item.Quote, item.RefID).Scan(&item.ID, &item.NodeID, &item.SourceID, &item.RefID, &item.Quote)
	return item, translate(err)
}

func requirePG(w http.ResponseWriter, s *Server) (*PGStore, bool) {
	p, ok := s.store.(*PGStore)
	if !ok {
		writeErr(w, 501, "NOT_IMPLEMENTED", "требуется PostgreSQL")
	}
	return p, ok
}
func sourceEvidenceError(w http.ResponseWriter, err error) {
	var validation *domain.TreeValidationError
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, 404, "NOT_FOUND", "запись не найдена")
	case errors.As(err, &validation):
		writeJSON(w, 422, validation)
	default:
		writeErr(w, 500, "INTERNAL", "не удалось выполнить запрос")
	}
}
func (s *Server) handleListSources(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP) {
		return
	}
	p, ok := requirePG(w, s)
	if !ok {
		return
	}
	items, err := p.listSources(r.Context())
	if err != nil {
		sourceEvidenceError(w, err)
		return
	}
	writeJSON(w, 200, items)
}
func (s *Server) handleGetSource(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP) {
		return
	}
	p, ok := requirePG(w, s)
	if !ok {
		return
	}
	item, err := p.getSource(r.Context(), r.PathValue("id"))
	if err != nil {
		sourceEvidenceError(w, err)
		return
	}
	writeJSON(w, 200, item)
}
func (s *Server) handleListEvidences(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP) {
		return
	}
	p, ok := requirePG(w, s)
	if !ok {
		return
	}
	items, err := p.listEvidences(r.Context(), r.URL.Query().Get("node_id"), r.URL.Query().Get("source_id"))
	if err != nil {
		sourceEvidenceError(w, err)
		return
	}
	writeJSON(w, 200, items)
}
func (s *Server) handleCreateEvidence(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP) {
		return
	}
	p, ok := requirePG(w, s)
	if !ok {
		return
	}
	var input struct {
		NodeID   string `json:"node_id"`
		SourceID string `json:"source_id"`
		RefID    string `json:"ref_id"`
		Quote    string `json:"quote"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, 400, "BAD_REQUEST", "некорректный JSON")
		return
	}
	item, err := p.createEvidence(r.Context(), input.NodeID, domain.Evidence{SourceID: input.SourceID, RefID: input.RefID, Quote: input.Quote})
	if err != nil {
		sourceEvidenceError(w, err)
		return
	}
	writeJSON(w, 201, item)
}
