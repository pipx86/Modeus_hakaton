package api

import (
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

var ErrTreeExists = errors.New("TREE_ALREADY_EXISTS")

type TreeWriter interface {
	SaveTree(context.Context, string, []domain.Node) error
}

func (s *Server) handleSaveTree(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	var nodes []domain.Node
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&nodes); err != nil {
		writeErr(w, 400, "BAD_REQUEST", "ожидается JSON-массив узлов")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeErr(w, 400, "BAD_REQUEST", "после дерева есть лишние данные")
		return
	}
	if err := domain.ValidateTree(nodes); err != nil {
		writeTreeError(w, err)
		return
	}
	writer, ok := s.store.(TreeWriter)
	if !ok {
		writeErr(w, 501, "NOT_IMPLEMENTED", "запись дерева не поддерживается")
		return
	}
	err := writer.SaveTree(r.Context(), r.PathValue("id"), nodes)
	if err != nil {
		writeTreeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func writeTreeError(w http.ResponseWriter, err error) {
	var validation *domain.TreeValidationError
	switch {
	case errors.As(err, &validation):
		writeJSON(w, 422, validation)
	case errors.Is(err, ErrNotFound):
		writeErr(w, 404, "NOT_FOUND", "исследование не найдено")
	case errors.Is(err, ErrTreeExists):
		writeErr(w, 409, "TREE_ALREADY_EXISTS", "дерево уже создано")
	default:
		writeErr(w, 500, "INTERNAL", "не удалось сохранить дерево")
	}
}
func (m *memStore) SaveTree(ctx context.Context, id string, nodes []domain.Node) error {
	if err := domain.ValidateTree(nodes); err != nil {
		return err
	}
	// Копируем вложенные данные, чтобы вызывающий код не мог изменить сохранённое дерево.
	data, err := json.Marshal(nodes)
	if err != nil {
		return err
	}
	var saved []domain.Node
	if err = json.Unmarshal(data, &saved); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[id]
	if !ok {
		return ErrNotFound
	}
	if len(m.nodes[id]) != 0 {
		return ErrTreeExists
	}
	for _, n := range saved {
		for _, e := range n.Evidence {
			if _, exists := m.evidences[e.ID]; exists {
				return &domain.TreeValidationError{Code: "DUPLICATE_EVIDENCE_ID", NodeID: n.ID, Details: "ID уже занят"}
			}
		}
	}
	for _, n := range saved {
		if n.ParentID == nil {
			v := n.ID
			run.RootNodeID = &v
		}
		for _, e := range n.Evidence {
			m.evidences[e.ID] = e
			m.runEvidenceIDs[id] = append(m.runEvidenceIDs[id], e.ID)
		}
	}
	m.nodes[id] = saved
	return nil
}
