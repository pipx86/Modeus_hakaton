package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/logging"

	"github.com/jackc/pgx/v5/pgtype"
)

// Проверка результата исследования методистом (карточки 18, 23 и 26).
//
// Решение хранится рядом с проверяемым объектом: статус проверки — на узле,
// решение по сопоставлению — на связи activity_program_links. Кандидатов
// сопоставления создаёт ядро системы (карточка 22); здесь только хранение и
// проверка, поэтому до решения связь остаётся proposed и не считается фактом.

var (
	// ErrInvalidProgram — программа не выбрана или её нет в справочнике.
	ErrInvalidProgram = errors.New("api: invalid education program")
	// ErrInvalidResultKind — неизвестный режим ожидаемого результата.
	ErrInvalidResultKind = errors.New("api: invalid result kind")
)

// ReviewNode сохраняет решение методиста по узлу дерева.
func (p *PGStore) ReviewNode(nodeID, status, comment, reviewer string) (*domain.NodeReviewResult, error) {
	if p == nil || p.Pool == nil {
		return nil, ErrServiceUnavailable
	}
	if !validUUID(nodeID) {
		return nil, ErrNotFound
	}
	var id pgtype.UUID
	if err := id.Scan(nodeID); err != nil || !id.Valid {
		return nil, ErrNotFound
	}
	ctx, cancel := operation()
	defer cancel()
	result := &domain.NodeReviewResult{}
	var reviewedAt pgtype.Timestamptz
	err := p.Pool.QueryRow(ctx, `UPDATE nodes
SET review_status = $2, review_comment = $3, reviewed_at = now(), reviewed_by = $4, updated_at = now()
WHERE id = $1
RETURNING id::text, review_status, review_comment, reviewed_at`,
		id, status, strings.TrimSpace(comment), strings.TrimSpace(reviewer)).
		Scan(&result.ID, &result.ReviewStatus, &result.ReviewComment, &reviewedAt)
	if err != nil {
		return nil, translate(err)
	}
	if reviewedAt.Valid {
		at := reviewedAt.Time
		result.ReviewedAt = &at
	}
	result.NodeID = result.ID
	return result, nil
}

// ListProgramLinks возвращает связи узлов исследования с элементами программы.
// statusFilter пустой означает «все статусы».
func (p *PGStore) ListProgramLinks(runID, statusFilter string) ([]domain.ProgramLink, error) {
	if p == nil || p.Pool == nil {
		return nil, ErrServiceUnavailable
	}
	if !validUUID(runID) {
		return nil, ErrNotFound
	}
	ctx, cancel := operation()
	defer cancel()
	rows, err := p.Pool.Query(ctx, programLinksQuery+`
WHERE l.run_id = $1 AND ($2::text = '' OR l.status = $2::text)
AND l.snapshot_id IS NOT DISTINCT FROM (SELECT id FROM snapshots WHERE run_id=$1 ORDER BY version DESC LIMIT 1)
ORDER BY l.created_at, l.id`, runID, strings.TrimSpace(statusFilter))
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	return scanProgramLinks(rows)
}

// GetProgramLink читает одну связь исследования.
func (p *PGStore) GetProgramLink(runID, linkID string) (*domain.ProgramLink, error) {
	if p == nil || p.Pool == nil {
		return nil, ErrServiceUnavailable
	}
	if !validUUID(runID) || !validUUID(linkID) {
		return nil, ErrNotFound
	}
	ctx, cancel := operation()
	defer cancel()
	rows, err := p.Pool.Query(ctx, programLinksQuery+`
WHERE l.run_id = $1 AND l.id = $2`, runID, linkID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	links, err := scanProgramLinks(rows)
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return nil, ErrNotFound
	}
	return &links[0], nil
}

// ReviewProgramLink сохраняет решение методиста по связи.
func (p *PGStore) ReviewProgramLink(runID, linkID, status, reason, reviewer string, target ...string) (*domain.ProgramLink, error) {
	if p == nil || p.Pool == nil {
		return nil, ErrServiceUnavailable
	}
	if !validUUID(runID) || !validUUID(linkID) {
		return nil, ErrNotFound
	}
	ctx, cancel := operation()
	defer cancel()
	// Причина решения дополняет объяснение системы, а не затирает его: методист
	// должен видеть, почему связь была предложена.
	competencyID, ksaID := "", ""
	if len(target) > 0 {
		competencyID = target[0]
	}
	if len(target) > 1 {
		ksaID = target[1]
	}
	if competencyID != "" && !validUUID(competencyID) || ksaID != "" && !validUUID(ksaID) {
		return nil, &domain.TreeValidationError{Code: "INVALID_PROGRAM_LINK_TARGET", Details: "нужны UUID компетенции и ЗУН"}
	}
	if competencyID == "" && ksaID != "" {
		return nil, &domain.TreeValidationError{Code: "INVALID_PROGRAM_LINK_TARGET", Details: "при выборе ЗУН нужна его компетенция"}
	}
	exec, err := p.Pool.Exec(ctx, `UPDATE activity_program_links
SET status = $3,
    reason = CASE WHEN btrim($4) = '' THEN reason ELSE left(reason || ' | методист: ' || btrim($4), 2000) END,
    competency_id = COALESCE(NULLIF($6,'')::uuid,competency_id),
    ksa_id = CASE WHEN $6 <> '' THEN NULLIF($7,'')::uuid ELSE ksa_id END,
    source = 'methodist',
    reviewed_by = btrim($5),
    reviewed_at = now(),
    updated_at = now()
	WHERE run_id = $1 AND id = $2`, runID, linkID, status, reason, reviewer, competencyID, ksaID)
	if err != nil {
		return nil, translate(err)
	}
	if exec.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return p.GetProgramLink(runID, linkID)
}

// ReviewSummary считает, сколько узлов, обоснований и связей уже проверено.
func (p *PGStore) ReviewSummary(runID string) (*domain.ReviewSummary, error) {
	if p == nil || p.Pool == nil {
		return nil, ErrServiceUnavailable
	}
	if !validUUID(runID) {
		return nil, ErrNotFound
	}
	if _, err := p.GetRun(runID); err != nil {
		return nil, err
	}
	ctx, cancel := operation()
	defer cancel()
	summary := &domain.ReviewSummary{}
	if err := p.Pool.QueryRow(ctx, `SELECT count(*),
       count(*) FILTER (WHERE review_status = 'pending'),
       count(*) FILTER (WHERE review_status = 'approved'),
       count(*) FILTER (WHERE review_status = 'rejected')
FROM nodes WHERE run_id = $1`, runID).
		Scan(&summary.Nodes.Total, &summary.Nodes.Pending, &summary.Nodes.Approved, &summary.Nodes.Rejected); err != nil {
		return nil, translate(err)
	}
	if err := p.Pool.QueryRow(ctx, `SELECT count(*),
       count(*) FILTER (WHERE COALESCE(e.status, 'pending') = 'pending'),
       count(*) FILTER (WHERE COALESCE(e.status, '') = 'approved'),
       count(*) FILTER (WHERE COALESCE(e.status, '') = 'rejected')
FROM evidence e JOIN nodes n ON n.id = e.node_id WHERE n.run_id = $1`, runID).
		Scan(&summary.Evidence.Total, &summary.Evidence.Pending, &summary.Evidence.Approved, &summary.Evidence.Rejected); err != nil {
		return nil, translate(err)
	}
	if err := p.Pool.QueryRow(ctx, `SELECT count(*),
       count(*) FILTER (WHERE status = 'proposed'),
       count(*) FILTER (WHERE status = 'approved'),
       count(*) FILTER (WHERE status = 'rejected')
FROM activity_program_links WHERE run_id = $1 AND snapshot_id IS NOT DISTINCT FROM (SELECT id FROM snapshots WHERE run_id=$1 ORDER BY version DESC LIMIT 1)`, runID).
		Scan(&summary.Links.Total, &summary.Links.Proposed, &summary.Links.Approved, &summary.Links.Rejected); err != nil {
		return nil, translate(err)
	}
	return summary, nil
}

// SetRunResult фиксирует ожидаемый результат исследования (карточка 25).
// programID обязателен только для режима program.
func (p *PGStore) SetRunResult(runID, kind, programID string) error {
	if p == nil || p.Pool == nil {
		return ErrServiceUnavailable
	}
	if !validUUID(runID) {
		return ErrNotFound
	}
	if kind == string(domain.ResultKindProgram) {
		if !validUUID(programID) {
			return ErrInvalidProgram
		}
		exists, err := p.programExists(programID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrInvalidProgram
		}
	}
	ctx, cancel := operation()
	defer cancel()
	_, err := p.Pool.Exec(ctx, `UPDATE runs SET result_kind = $2, program_id = $3, updated_at = now() WHERE id = $1`,
		runID, kind, nullableUUID(programID))
	return translate(err)
}

// programExists проверяет справочник программ до записи, чтобы вместо ошибки
// внешнего ключа клиент получил понятное 422.
func (p *PGStore) programExists(programID string) (bool, error) {
	ctx, cancel := operation()
	defer cancel()
	var exists bool
	if err := p.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM education_programs WHERE id = $1)`, programID).Scan(&exists); err != nil {
		return false, translate(err)
	}
	return exists, nil
}

// normalizeRunResult проверяет ожидаемый результат исследования. Пустое
// значение означает исторический режим «только описание деятельности».
func normalizeRunResult(kind, programID string) (string, string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	programID = strings.TrimSpace(programID)
	switch kind {
	case "":
		return string(domain.ResultKindActivity), "", nil
	case string(domain.ResultKindActivity):
		return string(domain.ResultKindActivity), "", nil
	case string(domain.ResultKindProgram):
		if programID == "" {
			return "", "", ErrInvalidProgram
		}
		return string(domain.ResultKindProgram), programID, nil
	default:
		return "", "", ErrInvalidResultKind
	}
}

// programLinksQuery — общий SELECT для чтения связи с подписями узла, программы
// и компетенции, чтобы интерфейсу не приходилось дозапрашивать справочники.
const programLinksQuery = `SELECT l.id::text, l.run_id::text, COALESCE(l.snapshot_id::text,''), COALESCE(s.version,0), l.node_id::text, n.title, n.type,
       l.program_id::text, p.title, l.competency_id::text, c.title,
       COALESCE(l.ksa_id::text, ''), COALESCE(k.title, ''),
       l.status, l.reason, l.source, l.reviewed_at, COALESCE(s.tree,'null'::jsonb)
FROM activity_program_links l
JOIN nodes n ON n.id = l.node_id
JOIN education_programs p ON p.id = l.program_id
JOIN program_competencies c ON c.id = l.competency_id
LEFT JOIN competency_ksa k ON k.id = l.ksa_id
LEFT JOIN snapshots s ON s.id=l.snapshot_id`

func scanProgramLinks(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}) ([]domain.ProgramLink, error) {
	out := []domain.ProgramLink{}
	for rows.Next() {
		var link domain.ProgramLink
		var reviewedAt pgtype.Timestamptz
		var tree []byte
		if err := rows.Scan(&link.ID, &link.RunID, &link.SnapshotID, &link.SnapshotVersion, &link.NodeID, &link.NodeTitle, &link.NodeType,
			&link.ProgramID, &link.ProgramTitle, &link.CompetencyID, &link.CompetencyTitle,
			&link.KsaID, &link.KsaTitle, &link.Status, &link.Reason, &link.Source, &reviewedAt, &tree); err != nil {
			return nil, err
		}
		if len(tree) > 0 && string(tree) != "null" {
			nodes, err := parseSnapshotTree(tree)
			if err != nil {
				return nil, err
			}
			for _, node := range nodes {
				if node.ID == link.NodeID {
					link.Evidence = node.Evidence
					break
				}
			}
		}
		if reviewedAt.Valid {
			at := reviewedAt.Time
			link.ReviewedAt = &at
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

// nullableUUID превращает пустую строку в NULL, чтобы не ловить ошибку
// приведения типа на необязательном program_id.
func nullableUUID(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

// handleReviewNode godoc
// @Summary      Review one activity tree node
// @Description  Stores the methodologist decision on a node: approved or rejected, with an optional comment. Evidence of the node is kept as is.
// @Tags         review
// @Accept       json
// @Produce      json
// @Param        id    path      string  true  "Node ID"
// @Param        body  body      object{status=string,comment=string}  true  "Review decision"
// @Success      200   {object}  domain.NodeReviewResult
// @Failure      400   {object}  errorBody
// @Failure      401   {object}  errorBody
// @Failure      403   {object}  errorBody
// @Failure      404   {object}  errorBody
// @Failure      422   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/nodes/{id}/review [post]
func (s *Server) handleReviewNode(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleSpecialist) {
		return
	}
	var req struct {
		Status  string `json:"status"`
		Comment string `json:"comment,omitempty"`
	}
	if !decodeReviewBody(w, r, &req) {
		return
	}
	if !domain.ValidNodeReviewDecision(req.Status) {
		writeErr(w, http.StatusUnprocessableEntity, "INVALID_REVIEW_STATUS", "status должен быть approved или rejected")
		return
	}
	if len([]rune(req.Comment)) > 2000 {
		writeErr(w, http.StatusUnprocessableEntity, "COMMENT_TOO_LONG", "комментарий длиннее 2000 символов")
		return
	}
	pg, ok := requirePG(w, s)
	if !ok {
		return
	}
	result, err := pg.ReviewNode(r.PathValue("id"), req.Status, req.Comment, auth.Subject(r.Context()))
	if err != nil {
		writeReviewError(w, err, "не удалось сохранить решение по узлу")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleListProgramLinks godoc
// @Summary      List activity-to-program links of a run
// @Description  Returns proposals and decisions that connect activity tree nodes with educational program competencies. Proposed links are not facts until the methodologist approves them.
// @Tags         review
// @Produce      json
// @Param        id      path      string  true  "Run ID"
// @Param        status  query     string  false  "Filter: proposed, approved, rejected"
// @Success      200  {array}   domain.ProgramLink
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/program-links [get]
func (s *Server) handleListProgramLinks(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser, auth.RoleSpecialist) {
		return
	}
	pg, ok := requirePG(w, s)
	if !ok {
		return
	}
	links, err := pg.ListProgramLinks(r.PathValue("id"), r.URL.Query().Get("status"))
	if err != nil {
		writeReviewError(w, err, "не удалось прочитать сопоставления")
		return
	}
	writeJSON(w, http.StatusOK, links)
}

// handleReviewProgramLink godoc
// @Summary      Review one activity-to-program link
// @Description  Approves or rejects a proposed link. The methodologist comment is appended to the system explanation instead of replacing it.
// @Tags         review
// @Accept       json
// @Produce      json
// @Param        id       path  string  true  "Run ID"
// @Param        link_id  path  string  true  "Link ID"
// @Param        body     body  object{status=string,reason=string}  true  "Review decision"
// @Success      200  {object}  domain.ProgramLink
// @Failure      400  {object}  errorBody
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      422  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/program-links/{link_id}/review [post]
func (s *Server) handleReviewProgramLink(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleSpecialist) {
		return
	}
	var req struct {
		Status       string `json:"status"`
		Reason       string `json:"reason,omitempty"`
		CompetencyID string `json:"competency_id,omitempty"`
		KsaID        string `json:"ksa_id,omitempty"`
	}
	if !decodeReviewBody(w, r, &req) {
		return
	}
	if !domain.ValidProgramLinkDecision(req.Status) {
		writeErr(w, http.StatusUnprocessableEntity, "INVALID_REVIEW_STATUS", "status должен быть approved или rejected")
		return
	}
	pg, ok := requirePG(w, s)
	if !ok {
		return
	}
	if req.CompetencyID != "" && !validUUID(req.CompetencyID) || req.KsaID != "" && !validUUID(req.KsaID) || req.CompetencyID == "" && req.KsaID != "" {
		writeErr(w, 422, "INVALID_PROGRAM_LINK_TARGET", "для исправления нужны UUID компетенции и связанного ЗУН")
		return
	}
	link, err := pg.ReviewProgramLink(r.PathValue("id"), r.PathValue("link_id"), req.Status, req.Reason, auth.Subject(r.Context()), req.CompetencyID, req.KsaID)
	if err != nil {
		writeReviewError(w, err, "не удалось сохранить решение по сопоставлению")
		return
	}
	writeJSON(w, http.StatusOK, link)
}

// handleReviewSummary godoc
// @Summary      Review progress of a research run
// @Description  Counts how many nodes, evidence items and program links are already reviewed, so the methodologist sees from one place what is checked and what is not.
// @Tags         review
// @Produce      json
// @Param        id   path      string  true  "Run ID"
// @Success      200  {object}  domain.ReviewSummary
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/review/summary [get]
func (s *Server) handleReviewSummary(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser, auth.RoleSpecialist) {
		return
	}
	pg, ok := requirePG(w, s)
	if !ok {
		return
	}
	summary, err := pg.ReviewSummary(r.PathValue("id"))
	if err != nil {
		writeReviewError(w, err, "не удалось посчитать прогресс проверки")
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// decodeReviewBody читает небольшое тело решения и различает пустое тело (EOF)
// от некорректного JSON.
func decodeReviewBody(w http.ResponseWriter, r *http.Request, dest any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "некорректный JSON")
		return false
	}
	return true
}

// writeReviewError переводит ошибки проверки в HTTP-ответы.
func writeReviewError(w http.ResponseWriter, err error, message string) {
	var validation *domain.TreeValidationError
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "NOT_FOUND", message)
	case errors.Is(err, ErrServiceUnavailable):
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "проверка требует PostgreSQL")
	case errors.As(err, &validation):
		writeErr(w, http.StatusUnprocessableEntity, "INVALID_PROGRAM_LINK_TARGET", "выбранная компетенция или ЗУН не относится к программе исследования")
	default:
		logging.Logger.Error("review_store_failed", "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", message)
	}
}
