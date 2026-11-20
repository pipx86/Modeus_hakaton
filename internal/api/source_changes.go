package api

import (
	"net/http"
	"time"

	"activity-descriptor/internal/auth"
)

// SourceChange is a possible impact: an attached source changed after an
// evidence item was created. A person must assess the significance.
type SourceChange struct {
	SourceID    string    `json:"source_id"`
	SourceTitle string    `json:"source_title"`
	NodeID      string    `json:"node_id"`
	NodeTitle   string    `json:"node_title"`
	EvidenceID  string    `json:"evidence_id"`
	ChangedAt   time.Time `json:"changed_at"`
}

func (p *PGStore) listSourceChanges(runID string) ([]SourceChange, error) {
	ctx, cancel := operation()
	defer cancel()
	rows, err := p.Pool.Query(ctx, `SELECT s.id::text, s.title, n.id::text, n.title, e.id::text, max(c.changed_at)
FROM evidence e JOIN nodes n ON n.id=e.node_id
JOIN sources s ON s.id=e.source_id
JOIN source_content_changes c ON c.source_id=s.id AND c.changed_at > e.created_at
WHERE n.run_id=$1
GROUP BY s.id, s.title, n.id, n.title, e.id
ORDER BY max(c.changed_at) DESC, e.id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SourceChange{}
	for rows.Next() {
		var item SourceChange
		if err = rows.Scan(&item.SourceID, &item.SourceTitle, &item.NodeID, &item.NodeTitle, &item.EvidenceID, &item.ChangedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Server) handleSourceChanges(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser, auth.RoleSpecialist) {
		return
	}
	runID := r.PathValue("id")
	if !s.allowInterviewAccess(w, r, runID) {
		return
	}
	pg, ok := requirePG(w, s)
	if !ok {
		return
	}
	items, err := pg.listSourceChanges(runID)
	if err != nil {
		writeRunCardsError(w, err, "не удалось получить изменения источников")
		return
	}
	writeJSON(w, http.StatusOK, items)
}
