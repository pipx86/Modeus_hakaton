package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

// runSourceContentReader is the optional finalizer capability implemented by
// PGStore: it returns the full content of the sources attached to a run.
type runSourceContentReader interface {
	ListRunSourceContents(ctx context.Context, runID string) ([]domain.Source, error)
}

var _ runSourceContentReader = (*PGStore)(nil)

// ListRunSourceContents returns the sources linked to a run through run_sources,
// including their content. It is used by the finalizer so the analyst can quote
// standard/vacancy materials; unlike ListRunSources it is not cheap, so the
// metadata-only method stays the default for read/export paths.
func (p *PGStore) ListRunSourceContents(ctx context.Context, runID string) ([]domain.Source, error) {
	if p == nil || p.Pool == nil {
		return nil, ErrServiceUnavailable
	}
	if !validUUID(runID) {
		return nil, ErrNotFound
	}
	var runUUID pgtype.UUID
	if err := runUUID.Scan(runID); err != nil || !runUUID.Valid {
		return nil, ErrNotFound
	}
	items, err := store.New(p.Pool).ListSourcesByRun(ctx, runUUID)
	if err != nil {
		return nil, translate(err)
	}
	out := make([]domain.Source, 0, len(items))
	for _, item := range items {
		sourceType := domain.SourceType(item.Type)
		out = append(out, domain.Source{
			ID:        item.ID.String(),
			Type:      sourceType,
			Title:     item.Title,
			Content:   item.Content.String,
			SourceURL: item.SourceUrl.String,
			Priority:  domain.SourcePriority(sourceType),
		})
	}
	return out, nil
}

// LinkRunSources attaches already existing sources to a run. Unknown or
// malformed ids are skipped: the finalizer uses this best-effort path to keep
// admin/global sources in run_sources without failing the run.
func (p *PGStore) LinkRunSources(ctx context.Context, runID string, sourceIDs []string) error {
	if p == nil || p.Pool == nil {
		return ErrServiceUnavailable
	}
	if !validUUID(runID) {
		return ErrNotFound
	}
	var runUUID pgtype.UUID
	if err := runUUID.Scan(runID); err != nil || !runUUID.Valid {
		return ErrNotFound
	}
	for _, sourceID := range sourceIDs {
		if !validUUID(strings.TrimSpace(sourceID)) {
			continue
		}
		var sourceUUID pgtype.UUID
		if err := sourceUUID.Scan(sourceID); err != nil || !sourceUUID.Valid {
			continue
		}
		if _, err := p.Pool.Exec(ctx, `INSERT INTO run_sources(run_id,source_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, runUUID, sourceUUID); err != nil {
			return translate(err)
		}
	}
	return nil
}

// AttachRunSource creates a new source bound to the run and links it through
// run_sources in one transaction. It is the live-flow entry point used to give
// a run its standard/vacancy material before the interview is finalized.
func (p *PGStore) AttachRunSource(ctx context.Context, runID string, src domain.Source) (*domain.Source, error) {
	if p == nil || p.Pool == nil {
		return nil, ErrServiceUnavailable
	}
	if !validUUID(runID) {
		return nil, ErrNotFound
	}
	if err := validateAttachableSource(src); err != nil {
		return nil, err
	}
	var runUUID pgtype.UUID
	if err := runUUID.Scan(runID); err != nil || !runUUID.Valid {
		return nil, ErrNotFound
	}

	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())

	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM runs WHERE id=$1)`, runUUID).Scan(&exists); err != nil {
		return nil, translate(err)
	}
	if !exists {
		return nil, ErrNotFound
	}

	var created domain.Source
	var createdID pgtype.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO sources(type,title,content,source_url,run_id)
VALUES($1,$2,$3,NULLIF($4,''),$5)
RETURNING id,type,title,COALESCE(content,''),COALESCE(source_url,''),created_at`,
		string(src.Type), src.Title, src.Content, src.SourceURL, runUUID).
		Scan(&createdID, &created.Type, &created.Title, &created.Content, &created.SourceURL, &created.CreatedAt); err != nil {
		return nil, translate(err)
	}
	created.ID = createdID.String()
	if _, err = tx.Exec(ctx, `INSERT INTO run_sources(run_id,source_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, runUUID, createdID); err != nil {
		return nil, translate(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, translate(err)
	}
	created.Priority = domain.SourcePriority(created.Type)
	return &created, nil
}

// validateAttachableSource limits the live attachment endpoint to the document
// types the analyst can quote. Interview and document sources are excluded:
// the interview source is created by the finalizer itself.
func validateAttachableSource(src domain.Source) error {
	if strings.TrimSpace(src.Title) == "" {
		return &domain.TreeValidationError{Code: "EMPTY_TITLE", Details: "нужно название источника"}
	}
	if strings.TrimSpace(src.Content) == "" {
		return &domain.TreeValidationError{Code: "EMPTY_CONTENT", Details: "нужен текст источника"}
	}
	switch src.Type {
	case domain.SourceStandard, domain.SourceVacancy:
		return nil
	default:
		return &domain.TreeValidationError{Code: "INVALID_SOURCE_TYPE", Details: "допустимы только standard и vacancy"}
	}
}

// handleAttachRunSource godoc
// @Summary      Attach a standard/vacancy source to a run
// @Description  Creates a standard or vacancy source and links it to the run through run_sources, so the analyst can quote it and the synthesizer can detect cross-source conflicts. The source is available to finalization immediately.
// @Tags         runs
// @Accept       json
// @Produce      json
// @Param        id    path      string  true  "Run ID"
// @Param        body  body      object{type=string,title=string,content=string,source_url=string}  true  "Source parameters"
// @Success      201   {object}  domain.Source
// @Failure      400   {object}  errorBody
// @Failure      401   {object}  errorBody
// @Failure      403   {object}  errorBody
// @Failure      404   {object}  errorBody
// @Failure      422   {object}  domain.TreeValidationError
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/sources [post]
func (s *Server) handleAttachRunSource(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	p, ok := requirePG(w, s)
	if !ok {
		return
	}

	var req struct {
		Type      domain.SourceType `json:"type"`
		Title     string            `json:"title"`
		Content   string            `json:"content"`
		SourceURL string            `json:"source_url,omitempty"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "некорректный JSON")
		return
	}

	created, err := p.AttachRunSource(r.Context(), r.PathValue("id"), domain.Source{
		Type:      req.Type,
		Title:     strings.TrimSpace(req.Title),
		Content:   strings.TrimSpace(req.Content),
		SourceURL: strings.TrimSpace(req.SourceURL),
	})
	if err != nil {
		var validation *domain.TreeValidationError
		switch {
		case errors.Is(err, ErrNotFound):
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "исследование не найдено")
		case errors.As(err, &validation):
			writeJSON(w, http.StatusUnprocessableEntity, validation)
		default:
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось привязать источник")
		}
		return
	}
	writeJSON(w, http.StatusCreated, created)
}
