package store

import (
	"context"
	"errors"
	"fmt"

	"activity-descriptor/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Database задаёт операции PostgreSQL, нужные хранилищам для запросов и транзакций.
type Database interface {
	db.DBTX
	Begin(context.Context) (pgx.Tx, error)
}

// Store предоставляет запросы sqlc и сохраняет дерево одной транзакцией.
type Store struct {
	*db.Queries
	pool Database
}

func New(pool Database) *Store { return &Store{Queries: db.New(pool), pool: pool} }

// TreeNode содержит узел и потомков; каждый узел требует подтверждение источником.
type TreeNode struct {
	ID          pgtype.UUID
	Title       string
	Description pgtype.Text
	Evidence    []TreeEvidence
	Children    []TreeNode
}

type TreeEvidence struct {
	ID       pgtype.UUID
	SourceID pgtype.UUID
	RefID    pgtype.Text
	Quote    string
	Approved bool
}

// SaveTree создаёт новое дерево исследования без корня; готовое дерево не заменяется.
// При ошибке узла, подтверждения или записи корня вся транзакция откатывается.
func (s *Store) SaveTree(ctx context.Context, runID pgtype.UUID, root TreeNode) error {
	if !runID.Valid {
		return errors.New("run ID is required")
	}
	if err := ValidateTree(root); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tree transaction: %w", err)
	}
	defer tx.Rollback(context.Background())
	q := s.Queries.WithTx(tx)
	var insert func(TreeNode, pgtype.UUID, int) error
	insert = func(n TreeNode, parent pgtype.UUID, depth int) error {
		types := [...]string{"role", "function", "task", "action"}
		if depth >= len(types) {
			return errors.New("tree exceeds role/function/task/action levels")
		}
		if !n.ID.Valid || len(n.Evidence) == 0 {
			return errors.New("every node needs an ID and evidence")
		}
		if _, err := q.CreateNode(ctx, db.CreateNodeParams{ID: n.ID, RunID: runID, ParentID: parent, Level: int16(depth), Type: types[depth], Title: n.Title, Description: n.Description}); err != nil {
			return fmt.Errorf("create node: %w", err)
		}
		for _, e := range n.Evidence {
			if !e.ID.Valid || !e.SourceID.Valid {
				return errors.New("evidence ID and source ID are required")
			}
			if _, err := q.CreateEvidence(ctx, db.CreateEvidenceParams{ID: e.ID, NodeID: n.ID, SourceID: e.SourceID, Quote: e.Quote, Approved: e.Approved, RefID: e.RefID}); err != nil {
				return fmt.Errorf("create evidence: %w", err)
			}
		}
		for _, child := range n.Children {
			if err := insert(child, n.ID, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := insert(root, pgtype.UUID{}, 0); err != nil {
		return err
	}
	count, err := q.SetRunRoot(ctx, db.SetRunRootParams{ID: runID, RootNodeID: root.ID})
	if err != nil {
		return fmt.Errorf("set root: %w", err)
	}
	if count != 1 {
		return errors.New("run does not exist or already has a root")
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tree: %w", err)
	}
	return nil
}
