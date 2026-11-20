package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// PGStore реализует контракт API и сохранение состояния оркестратора в PostgreSQL.
type PGStore struct{ Pool store.Database }

func NewPGStore(pool store.Database) *PGStore { return &PGStore{Pool: pool} }
func operation() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}
func validUUID(s string) bool { var id pgtype.UUID; return id.Scan(s) == nil && id.Valid }
func translate(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var p *pgconn.PgError
	if errors.As(err, &p) {
		switch p.ConstraintName {
		case "EMPTY_TITLE", "ORPHAN_NODE", "CYCLE_DETECTED", "INVALID_TREE_DEPTH", "DUPLICATE_SIBLING_TITLE", "MISSING_EVIDENCE", "PROGRAM_LINK_COMPETENCY_MISMATCH", "PROGRAM_LINK_KSA_MISMATCH":
			return &domain.TreeValidationError{Code: p.ConstraintName, Details: "дерево нарушает ограничение базы"}
		}
		if p.Code == "23505" {
			return &domain.TreeValidationError{Code: "DUPLICATE_ID", Details: "идентификатор узла или подтверждения уже используется"}
		}
		if p.Code == "23503" {
			return &domain.TreeValidationError{Code: "INVALID_REFERENCE", Details: "узел, источник или исследование не существует"}
		}
	}
	return err
}
func (p *PGStore) CreateRun(profession string) (*domain.Run, error) {
	ctx, cancel := operation()
	defer cancel()
	profession = strings.TrimSpace(profession)
	if profession == "" {
		return nil, &domain.TreeValidationError{Code: "EMPTY_PROFESSION", Details: "профессия не задана"}
	}
	r := &domain.Run{}
	err := p.Pool.QueryRow(ctx, "INSERT INTO runs(profession) VALUES($1) RETURNING id::text,profession,status,stage,type,COALESCE(created_by::text,'')", profession).Scan(&r.ID, &r.Profession, &r.Status, &r.Stage, &r.Type, &r.CreatedBy)
	return r, translate(err)
}

// CreateRunForUser creates a run owned by the authenticated account.
func (p *PGStore) CreateRunForUser(profession, userID string) (*domain.Run, error) {
	ctx, cancel := operation()
	defer cancel()
	profession = strings.TrimSpace(profession)
	if profession == "" {
		return nil, &domain.TreeValidationError{Code: "EMPTY_PROFESSION", Details: "профессия не задана"}
	}
	if !validUUID(userID) {
		return p.CreateRun(profession)
	}
	r := &domain.Run{}
	err := p.Pool.QueryRow(ctx, "INSERT INTO runs(profession,created_by) VALUES($1,$2) RETURNING id::text,profession,status,stage,type,COALESCE(created_by::text,'')", profession, userID).Scan(&r.ID, &r.Profession, &r.Status, &r.Stage, &r.Type, &r.CreatedBy)
	return r, translate(err)
}
func (p *PGStore) GetRun(id string) (*domain.Run, error) {
	if !validUUID(id) {
		return nil, ErrNotFound
	}
	ctx, cancel := operation()
	defer cancel()
	r := &domain.Run{}
	var programID string
	err := p.Pool.QueryRow(ctx, "SELECT id::text,profession,status,stage,type,COALESCE(created_by::text,''),root_node_id::text,started_at,finished_at,result_kind,COALESCE(program_id::text,'') FROM runs WHERE id=$1", id).Scan(&r.ID, &r.Profession, &r.Status, &r.Stage, &r.Type, &r.CreatedBy, &r.RootNodeID, &r.StartedAt, &r.FinishedAt, &r.ResultKind, &programID)
	if err == nil && programID != "" {
		r.ProgramID = &programID
	}
	return r, translate(err)
}

// ActiveInterviewID returns the current respondent interview for evidence
// provenance. A missing session is normal for batch/non-chat runs.
func (p *PGStore) ActiveInterviewID(ctx context.Context, runID string) string {
	if p == nil || p.Pool == nil || !validUUID(runID) {
		return ""
	}
	var id string
	if err := p.Pool.QueryRow(ctx, `SELECT interview_id::text FROM run_chat_sessions WHERE run_id=$1`, runID).Scan(&id); err != nil {
		return ""
	}
	return id
}
func (p *PGStore) GetTree(id string) ([]domain.Node, error) {
	if _, err := p.GetRun(id); err != nil {
		return nil, err
	}
	ctx, cancel := operation()
	defer cancel()
	rows, err := p.Pool.Query(ctx, `SELECT n.id::text,n.type,n.title,COALESCE(n.description,''),n.parent_id::text,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',e.id,'node_id',e.node_id,'source_id',e.source_id,'ref_id',COALESCE(e.ref_id,''),'quote',e.quote,'status',COALESCE(e.status,'pending'),'claim_origin',e.claim_origin,'interview_id',e.interview_id) ORDER BY e.id) FROM evidence e WHERE e.node_id=n.id),'[]'::jsonb),
 COALESCE(n.zun,'null'::jsonb),
 COALESCE(n.review_status,'pending'),COALESCE(n.review_comment,''),n.reviewed_at
 FROM nodes n WHERE n.run_id=$1 ORDER BY n.level,n.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Node{}
	for rows.Next() {
		var n domain.Node
		var data []byte
		var zun []byte
		var reviewedAt pgtype.Timestamptz
		if err = rows.Scan(&n.ID, &n.Type, &n.Title, &n.Description, &n.ParentID, &data, &zun, &n.ReviewStatus, &n.ReviewComment, &reviewedAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &n.Evidence); err != nil {
			return nil, err
		}
		if reviewedAt.Valid {
			at := reviewedAt.Time
			n.ReviewedAt = &at
		}
		if len(zun) > 0 && string(zun) != "null" {
			var parsed domain.Zun
			if err = json.Unmarshal(zun, &parsed); err == nil {
				n.Zun = &parsed
			}
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func (p *PGStore) GetAnswers(id string) ([]domain.Evidence, error) {
	if _, err := p.GetRun(id); err != nil {
		return nil, err
	}
	ctx, cancel := operation()
	defer cancel()
	rows, err := p.Pool.Query(ctx, "SELECT e.id::text,e.node_id::text,e.source_id::text,COALESCE(e.ref_id,''),e.quote,COALESCE(e.status,'pending'),e.claim_origin,COALESCE(e.interview_id::text,'') FROM evidence e JOIN nodes n ON n.id=e.node_id WHERE n.run_id=$1 ORDER BY e.id", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Evidence{}
	for rows.Next() {
		var e domain.Evidence
		var st string
		if err = rows.Scan(&e.ID, &e.NodeID, &e.SourceID, &e.RefID, &e.Quote, &st, &e.ClaimOrigin, &e.InterviewID); err != nil {
			return nil, err
		}
		e.Status = domain.EvidenceStatus(st)
		out = append(out, e)
	}
	return out, rows.Err()
}
func (p *PGStore) ApproveEvidence(id string) error {
	if !validUUID(id) {
		return ErrNotFound
	}
	ctx, cancel := operation()
	defer cancel()
	// status + approved; trigger syncs approved from status when column exists
	tag, err := p.Pool.Exec(ctx, `UPDATE evidence SET status='approved', approved=true WHERE id=$1 AND COALESCE(status,'pending') = 'pending'`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var status string
	err = p.Pool.QueryRow(ctx, `SELECT COALESCE(status,'pending') FROM evidence WHERE id=$1`, id).Scan(&status)
	if err != nil {
		return translate(err)
	}
	if status == "approved" {
		return ErrAlreadyApproved
	}
	if status == "rejected" {
		return ErrAlreadyRejected
	}
	return ErrAlreadyApproved
}

func (p *PGStore) RejectEvidence(id string) error {
	if !validUUID(id) {
		return ErrNotFound
	}
	ctx, cancel := operation()
	defer cancel()
	tag, err := p.Pool.Exec(ctx, `UPDATE evidence SET status='rejected', approved=false WHERE id=$1 AND COALESCE(status,'pending') = 'pending'`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var status string
	err = p.Pool.QueryRow(ctx, `SELECT COALESCE(status,'pending') FROM evidence WHERE id=$1`, id).Scan(&status)
	if err != nil {
		return translate(err)
	}
	if status == "rejected" {
		return ErrAlreadyRejected
	}
	if status == "approved" {
		return ErrAlreadyApproved
	}
	return ErrAlreadyRejected
}
func (p *PGStore) SetStage(id string, stage domain.Stage) error {
	if !validUUID(id) {
		return ErrNotFound
	}
	ctx, cancel := operation()
	defer cancel()
	tag, err := p.Pool.Exec(ctx, `UPDATE runs SET stage=$2,status=CASE WHEN $2 IN ('done','failed') THEN $2 WHEN $2='created' THEN 'pending' ELSE 'running' END,
 started_at=CASE WHEN $2<>'created' THEN COALESCE(started_at,now()) ELSE started_at END,
 finished_at=CASE WHEN $2 IN ('done','failed') THEN now() ELSE finished_at END,updated_at=now() WHERE id=$1`, id, string(stage))
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}
func (p *PGStore) SetStatus(id string, status domain.RunStatus) error {
	if !validUUID(id) {
		return ErrNotFound
	}
	ctx, cancel := operation()
	defer cancel()
	tag, err := p.Pool.Exec(ctx, `UPDATE runs SET status=$2,stage=CASE WHEN $2='degraded' THEN 'done' WHEN $2 IN ('done','failed') THEN $2 ELSE stage END,
 started_at=CASE WHEN $2='running' THEN COALESCE(started_at,now()) ELSE started_at END,
 finished_at=CASE WHEN $2 IN ('done','failed','degraded') THEN now() ELSE finished_at END,updated_at=now() WHERE id=$1`, id, string(status))
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (p *PGStore) SetRunType(id string, runType domain.RunType) error {
	if !validUUID(id) {
		return ErrNotFound
	}
	if runType != domain.RunTypeBatch && runType != domain.RunTypeChat && runType != domain.RunTypeLearning {
		return errors.New("INVALID_RUN_TYPE")
	}
	ctx, cancel := operation()
	defer cancel()
	tag, err := p.Pool.Exec(ctx, `UPDATE runs SET type=$2,updated_at=now() WHERE id=$1`, id, string(runType))
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (p *PGStore) Save(r *domain.Run) error {
	if r == nil || !validUUID(r.ID) {
		return ErrNotFound
	}
	ctx, cancel := operation()
	defer cancel()
	tag, err := p.Pool.Exec(ctx, `UPDATE runs SET status=$2,stage=$3,
 started_at=CASE WHEN $2='running' THEN COALESCE(started_at,now()) ELSE started_at END,
 finished_at=CASE WHEN $2 IN ('done','failed','degraded') THEN now() ELSE finished_at END,updated_at=now() WHERE id=$1`, r.ID, string(r.Status), string(r.Stage))
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

// SaveTree сохраняет новое дерево атомарно; существующий результат не заменяется.

func (p *PGStore) SaveTree(ctx context.Context, id string, nodes []domain.Node) error {
	if err := domain.ValidateTree(nodes); err != nil {
		return err
	}
	run, err := p.GetRun(id)
	if err != nil {
		return err
	}
	if run.RootNodeID != nil {
		return ErrTreeExists
	}
	var runID pgtype.UUID
	if err = runID.Scan(id); err != nil {
		return ErrNotFound
	}
	byParent := map[string][]domain.Node{}
	var root domain.Node
	for _, n := range nodes {
		if !validUUID(n.ID) {
			return &domain.TreeValidationError{Code: "INVALID_NODE_ID", NodeID: n.ID, Details: "ожидается UUID"}
		}
		for _, e := range n.Evidence {
			if !validUUID(e.ID) || !validUUID(e.SourceID) {
				return &domain.TreeValidationError{Code: "INVALID_EVIDENCE", NodeID: n.ID, Details: "ожидаются UUID подтверждения и источника"}
			}
			if e.InterviewID != "" && !validUUID(e.InterviewID) {
				return &domain.TreeValidationError{Code: "INVALID_EVIDENCE", NodeID: n.ID, Details: "interview_id должен быть UUID"}
			}
		}
		if n.ParentID == nil {
			root = n
		} else {
			byParent[*n.ParentID] = append(byParent[*n.ParentID], n)
		}
	}
	var convert func(domain.Node) store.TreeNode
	convert = func(n domain.Node) store.TreeNode {
		result := store.TreeNode{Title: n.Title, Description: pgtype.Text{String: n.Description, Valid: n.Description != ""}}
		result.ID.Scan(n.ID)
		for _, e := range n.Evidence {
			interviewUUID := pgtype.UUID{}
			if e.InterviewID != "" {
				_ = interviewUUID.Scan(e.InterviewID)
			}
			item := store.TreeEvidence{Quote: e.Quote, RefID: pgtype.Text{String: e.RefID, Valid: e.RefID != ""}, ClaimOrigin: string(e.ClaimOrigin), InterviewID: interviewUUID}
			item.ID.Scan(e.ID)
			item.SourceID.Scan(e.SourceID)
			result.Evidence = append(result.Evidence, item)
		}
		for _, child := range byParent[n.ID] {
			result.Children = append(result.Children, convert(child))
		}
		return result
	}
	err = store.New(p.Pool).SaveTree(ctx, runID, convert(root))
	if err != nil {
		var v *store.TreeValidationError
		if errors.As(err, &v) {
			return &domain.TreeValidationError{Code: v.Code, Details: v.Details}
		}
		if current, e := p.GetRun(id); e == nil && current.RootNodeID != nil {
			return ErrTreeExists
		}
	}
	if err == nil {
		// ЗУН не входит в sqlc-вставку узлов, сохраняем отдельным UPDATE.
		for _, n := range nodes {
			if n.Zun == nil {
				continue
			}
			payload, marshalErr := json.Marshal(n.Zun)
			if marshalErr != nil {
				continue
			}
			_, _ = p.Pool.Exec(ctx, `UPDATE nodes SET zun=$1 WHERE id=$2`, payload, n.ID)
		}
	}
	return translate(err)
}

// TreeExists позволяет безопасно продолжить исследование после записи дерева.
func (p *PGStore) TreeExists(ctx context.Context, id string) (bool, error) {
	if !validUUID(id) {
		return false, ErrNotFound
	}
	var found bool
	err := p.Pool.QueryRow(ctx, "SELECT root_node_id IS NOT NULL FROM runs WHERE id=$1", id).Scan(&found)
	return found, translate(err)
}

// ListIncompleteRuns возвращает исследования, прерванные при запуске или выполнении.
func (p *PGStore) ListIncompleteRuns() ([]*domain.Run, error) {
	ctx, cancel := operation()
	defer cancel()

	rows, err := p.Pool.Query(ctx, `
		SELECT id::text, profession, status::text, stage::text, type::text, started_at, finished_at
		FROM runs
		WHERE status IN ('pending', 'running') AND stage <> 'done' AND type = 'batch'
		ORDER BY created_at ASC`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	var out []*domain.Run
	for rows.Next() {
		var r domain.Run
		if err := rows.Scan(&r.ID, &r.Profession, &r.Status, &r.Stage, &r.Type, &r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *PGStore) UpdateNode(nodeID string, patch NodePatch) (*domain.Node, error) {
	if !validUUID(nodeID) {
		return nil, ErrNotFound
	}
	if patch.Title != nil && strings.TrimSpace(*patch.Title) == "" {
		return nil, &domain.TreeValidationError{Code: "EMPTY_TITLE", NodeID: nodeID, Details: "название пустое"}
	}
	if patch.Evidence != nil {
		if len(*patch.Evidence) == 0 {
			return nil, &domain.TreeValidationError{Code: "MISSING_EVIDENCE", NodeID: nodeID, Details: "нужно хотя бы одно подтверждение"}
		}
		seen := make(map[string]bool, len(*patch.Evidence))
		for _, e := range *patch.Evidence {
			if !validUUID(e.ID) || !validUUID(e.SourceID) || strings.TrimSpace(e.Quote) == "" || (e.RefID != "" && strings.TrimSpace(e.RefID) == "") {
				return nil, &domain.TreeValidationError{Code: "INVALID_EVIDENCE", NodeID: nodeID, Details: "нужны UUID и цитата"}
			}
			id := strings.ToLower(e.ID)
			if seen[id] {
				return nil, &domain.TreeValidationError{Code: "DUPLICATE_EVIDENCE_ID", NodeID: nodeID, Details: "идентификатор подтверждения повторяется"}
			}
			seen[id] = true
		}
	}
	ctx, cancel := operation()
	defer cancel()
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	var n domain.Node
	var evidence []byte
	err = tx.QueryRow(ctx, `UPDATE nodes SET title=COALESCE($2,title), description=COALESCE($3,description), updated_at=now() WHERE id=$1 RETURNING id::text,type,title,COALESCE(description,''),parent_id::text`, nodeID, patch.Title, patch.Description).Scan(&n.ID, &n.Type, &n.Title, &n.Description, &n.ParentID)
	if err != nil {
		return nil, translate(err)
	}
	if patch.Evidence != nil {
		ids := make([]string, 0, len(*patch.Evidence))
		for _, e := range *patch.Evidence {
			tag, err := tx.Exec(ctx, `INSERT INTO evidence(id,node_id,source_id,quote,ref_id,claim_origin,interview_id) VALUES($1,$2,$3,$4,NULLIF($5,''),COALESCE(NULLIF($6,''),'unclassified'),NULLIF($7,'')::uuid)
 ON CONFLICT (id) DO UPDATE SET source_id=EXCLUDED.source_id, quote=EXCLUDED.quote, ref_id=EXCLUDED.ref_id,
 claim_origin=EXCLUDED.claim_origin, interview_id=EXCLUDED.interview_id,
 status=CASE WHEN evidence.source_id=EXCLUDED.source_id AND evidence.quote=EXCLUDED.quote AND evidence.ref_id IS NOT DISTINCT FROM EXCLUDED.ref_id THEN evidence.status ELSE 'pending' END,
 approved=CASE WHEN evidence.source_id=EXCLUDED.source_id AND evidence.quote=EXCLUDED.quote AND evidence.ref_id IS NOT DISTINCT FROM EXCLUDED.ref_id THEN evidence.approved ELSE false END
 WHERE evidence.node_id=EXCLUDED.node_id`, e.ID, nodeID, e.SourceID, e.Quote, e.RefID, e.ClaimOrigin, e.InterviewID)
			if err != nil {
				return nil, translate(err)
			}
			if tag.RowsAffected() != 1 {
				return nil, &domain.TreeValidationError{Code: "DUPLICATE_EVIDENCE_ID", NodeID: nodeID, Details: "подтверждение принадлежит другому узлу"}
			}
			ids = append(ids, e.ID)
		}
		if _, err = tx.Exec(ctx, `DELETE FROM evidence WHERE node_id=$1 AND NOT (id = ANY($2::uuid[]))`, nodeID, ids); err != nil {
			return nil, translate(err)
		}
	}
	err = tx.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id,'node_id',node_id,'source_id',source_id,'ref_id',COALESCE(ref_id,''),'quote',quote,'status',COALESCE(status,'pending'),'claim_origin',claim_origin,'interview_id',interview_id) ORDER BY id),'[]'::jsonb) FROM evidence WHERE node_id=$1`, nodeID).Scan(&evidence)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(evidence, &n.Evidence); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, translate(err)
	}
	return &n, nil
}

func (p *PGStore) BulkApproveEvidence(ids []string) (int, int, error) {
	ctx, cancel := operation()
	defer cancel()
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(context.Background())
	approved := 0
	for _, id := range ids {
		if !validUUID(id) {
			continue
		}
		tag, err := tx.Exec(ctx, `UPDATE evidence SET status='approved', approved=true WHERE id=$1 AND COALESCE(status,'pending')='pending'`, id)
		if err != nil {
			return 0, 0, err
		}
		approved += int(tag.RowsAffected())
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return approved, len(ids) - approved, nil
}

func (p *PGStore) DeleteSource(sourceID string) error {
	if !validUUID(sourceID) {
		return ErrNotFound
	}
	ctx, cancel := operation()
	defer cancel()
	tag, err := p.Pool.Exec(ctx, `DELETE FROM sources WHERE id=$1`, sourceID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return ErrConflict
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *PGStore) DeleteRun(runID string) error {
	if !validUUID(runID) {
		return ErrNotFound
	}
	ctx, cancel := operation()
	defer cancel()
	tag, err := p.Pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, runID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *PGStore) ListRuns() ([]*domain.Run, error) {
	ctx, cancel := operation()
	defer cancel()
	rows, err := p.Pool.Query(ctx, `SELECT id::text,profession,status,stage,type,COALESCE(created_by::text,''),root_node_id::text,started_at,finished_at FROM runs ORDER BY created_at DESC,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []*domain.Run{}
	for rows.Next() {
		var r domain.Run
		if err = rows.Scan(&r.ID, &r.Profession, &r.Status, &r.Stage, &r.Type, &r.CreatedBy, &r.RootNodeID, &r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		result = append(result, &r)
	}
	return result, rows.Err()
}

// ListGlobalSources возвращает материалы, загруженные в админке (run_id IS NULL).
func (p *PGStore) ListGlobalSources(limit int) ([]domain.Source, error) {
	if limit <= 0 || limit > 50 {
		limit = 5
	}
	ctx, cancel := operation()
	defer cancel()
	rows, err := p.Pool.Query(ctx, `SELECT id::text,type,title,COALESCE(content,''),COALESCE(source_url,''),created_at
FROM sources WHERE run_id IS NULL AND type <> 'vacancy' ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	out := []domain.Source{}
	for rows.Next() {
		var src domain.Source
		if err := rows.Scan(&src.ID, &src.Type, &src.Title, &src.Content, &src.SourceURL, &src.CreatedAt); err != nil {
			return nil, translate(err)
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

func (p *PGStore) CreateSource(src domain.Source) (*domain.Source, error) {
	ctx, cancel := operation()
	defer cancel()
	var result domain.Source
	err := p.Pool.QueryRow(ctx, `INSERT INTO sources(type,title,content,source_url) VALUES($1,$2,$3,NULLIF($4,'')) RETURNING id::text,type,title,COALESCE(content,''),COALESCE(source_url,''),created_at`, src.Type, src.Title, src.Content, src.SourceURL).Scan(&result.ID, &result.Type, &result.Title, &result.Content, &result.SourceURL, &result.CreatedAt)
	if err != nil {
		return nil, translate(err)
	}
	return &result, nil
}
