package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/store"
	"activity-descriptor/internal/store/db"

	"github.com/jackc/pgx/v5/pgtype"
)

type snapshotResponse struct {
	ID        pgtype.UUID        `json:"id"`
	RunID     pgtype.UUID        `json:"run_id"`
	Version   int32              `json:"version"`
	Tree      json.RawMessage    `json:"tree"`
	CreatedAt pgtype.Timestamptz `json:"created_at"`
}

func presentSnapshot(s db.Snapshot) snapshotResponse {
	return snapshotResponse{ID: s.ID, RunID: s.RunID, Version: s.Version, Tree: json.RawMessage(s.Tree), CreatedAt: s.CreatedAt}
}

func (p *PGStore) createSnapshot(ctx context.Context, id string) (snapshotResponse, error) {
	run, err := p.GetRun(id)
	if err != nil {
		return snapshotResponse{}, err
	}
	if run.RootNodeID == nil {
		return snapshotResponse{}, ErrConflict
	}
	var runID pgtype.UUID
	if err = runID.Scan(id); err != nil {
		return snapshotResponse{}, ErrNotFound
	}
	result, err := store.New(p.Pool).SaveSnapshot(ctx, runID)
	if err != nil {
		return snapshotResponse{}, translate(err)
	}
	return presentSnapshot(result), nil
}

func (p *PGStore) listSnapshots(ctx context.Context, id string) ([]snapshotResponse, error) {
	if _, err := p.GetRun(id); err != nil {
		return nil, err
	}
	var runID pgtype.UUID
	if err := runID.Scan(id); err != nil {
		return nil, ErrNotFound
	}
	items, err := store.New(p.Pool).ListSnapshotsByRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	result := make([]snapshotResponse, 0, len(items))
	for _, item := range items {
		result = append(result, presentSnapshot(item))
	}
	return result, nil
}

func (p *PGStore) getSnapshot(ctx context.Context, id, snapshotID string) (snapshotResponse, error) {
	var runID, snapID pgtype.UUID
	if runID.Scan(id) != nil || !runID.Valid || snapID.Scan(snapshotID) != nil || !snapID.Valid {
		return snapshotResponse{}, ErrNotFound
	}
	item, err := store.New(p.Pool).GetSnapshot(ctx, db.GetSnapshotParams{RunID: runID, ID: snapID})
	if err != nil {
		return snapshotResponse{}, translate(err)
	}
	return presentSnapshot(item), nil
}

func snapshotError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, 404, "NOT_FOUND", "исследование или снимок не найден")
	case errors.Is(err, ErrConflict):
		writeErr(w, 409, "TREE_MISSING", "у исследования нет сохранённого дерева")
	default:
		writeErr(w, 500, "INTERNAL", "не удалось выполнить операцию со снимком")
	}
}

func (s *Server) handleCreateSnapshot(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP) {
		return
	}
	p, ok := s.store.(*PGStore)
	if !ok {
		writeErr(w, 501, "NOT_IMPLEMENTED", "для снимков требуется PostgreSQL")
		return
	}
	item, err := p.createSnapshot(r.Context(), r.PathValue("id"))
	if err != nil {
		snapshotError(w, err)
		return
	}
	writeJSON(w, 201, item)
}
func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleGuest) {
		return
	}
	p, ok := s.store.(*PGStore)
	if !ok {
		writeErr(w, 501, "NOT_IMPLEMENTED", "для снимков требуется PostgreSQL")
		return
	}
	items, err := p.listSnapshots(r.Context(), r.PathValue("id"))
	if err != nil {
		snapshotError(w, err)
		return
	}
	writeJSON(w, 200, items)
}
func (s *Server) handleGetSnapshot(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleGuest) {
		return
	}
	p, ok := s.store.(*PGStore)
	if !ok {
		writeErr(w, 501, "NOT_IMPLEMENTED", "для снимков требуется PostgreSQL")
		return
	}
	item, err := p.getSnapshot(r.Context(), r.PathValue("id"), r.PathValue("snapshot_id"))
	if err != nil {
		snapshotError(w, err)
		return
	}
	writeJSON(w, 200, item)
}
