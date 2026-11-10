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
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore реализует контракт API и сохранение состояния оркестратора в PostgreSQL.
type PGStore struct{ Pool *pgxpool.Pool }

func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{Pool: pool} }
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
		case "EMPTY_TITLE", "ORPHAN_NODE", "CYCLE_DETECTED", "INVALID_TREE_DEPTH", "DUPLICATE_SIBLING_TITLE", "MISSING_EVIDENCE":
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
	err := p.Pool.QueryRow(ctx, "INSERT INTO runs(profession) VALUES($1) RETURNING id::text,profession,status,stage", profession).Scan(&r.ID, &r.Profession, &r.Status, &r.Stage)
	return r, translate(err)
}
func (p *PGStore) GetRun(id string) (*domain.Run, error) {
	if !validUUID(id) {
		return nil, ErrNotFound
	}
	ctx, cancel := operation()
	defer cancel()
	r := &domain.Run{}
	err := p.Pool.QueryRow(ctx, "SELECT id::text,profession,status,stage,root_node_id::text,started_at,finished_at FROM runs WHERE id=$1", id).Scan(&r.ID, &r.Profession, &r.Status, &r.Stage, &r.RootNodeID, &r.StartedAt, &r.FinishedAt)
	return r, translate(err)
}
func (p *PGStore) GetTree(id string) ([]domain.Node, error) {
	if _, err := p.GetRun(id); err != nil {
		return nil, err
	}
	ctx, cancel := operation()
	defer cancel()
	rows, err := p.Pool.Query(ctx, `SELECT n.id::text,n.type,n.title,COALESCE(n.description,''),n.parent_id::text,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',e.id,'source_id',e.source_id,'quote',e.quote) ORDER BY e.id) FROM evidence e WHERE e.node_id=n.id),'[]'::jsonb)
 FROM nodes n WHERE n.run_id=$1 ORDER BY n.level,n.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Node{}
	for rows.Next() {
		var n domain.Node
		var data []byte
		if err = rows.Scan(&n.ID, &n.Type, &n.Title, &n.Description, &n.ParentID, &data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &n.Evidence); err != nil {
			return nil, err
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
	rows, err := p.Pool.Query(ctx, "SELECT e.id::text,e.source_id::text,e.quote FROM evidence e JOIN nodes n ON n.id=e.node_id WHERE n.run_id=$1 ORDER BY e.id", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Evidence{}
	for rows.Next() {
		var e domain.Evidence
		if err = rows.Scan(&e.ID, &e.SourceID, &e.Quote); err != nil {
			return nil, err
		}
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
	tag, err := p.Pool.Exec(ctx, "UPDATE evidence SET approved=true WHERE id=$1 AND NOT approved", id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var exists bool
	if err = p.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM evidence WHERE id=$1)", id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrAlreadyApproved
	}
	return ErrNotFound
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
	tag, err := p.Pool.Exec(ctx, `UPDATE runs SET status=$2,stage=CASE WHEN $2 IN ('done','failed') THEN $2 ELSE stage END,
 started_at=CASE WHEN $2='running' THEN COALESCE(started_at,now()) ELSE started_at END,
 finished_at=CASE WHEN $2 IN ('done','failed') THEN now() ELSE finished_at END,updated_at=now() WHERE id=$1`, id, string(status))
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
 finished_at=CASE WHEN $2 IN ('done','failed') THEN now() ELSE finished_at END,updated_at=now() WHERE id=$1`, r.ID, string(r.Status), string(r.Stage))
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
			item := store.TreeEvidence{Quote: e.Quote}
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
	return translate(err)
}
