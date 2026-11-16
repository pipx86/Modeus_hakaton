package store

import (
	"context"
	"errors"
	"fmt"

	"activity-descriptor/internal/store/db"

	"github.com/jackc/pgx/v5/pgtype"
)

// SaveSnapshot сохраняет текущее дерево и сведения о его подтверждениях.
// Запись исследования блокируется до конца транзакции, чтобы параллельные
// снимки одного исследования получали разные номера версий.
func (s *Store) SaveSnapshot(ctx context.Context, runID pgtype.UUID) (db.Snapshot, error) {
	if !runID.Valid {
		return db.Snapshot{}, errors.New("требуется идентификатор исследования")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.Snapshot{}, fmt.Errorf("начать транзакцию снимка: %w", err)
	}
	defer tx.Rollback(context.Background())
	q := s.Queries.WithTx(tx)
	run, err := q.LockRunForSnapshot(ctx, runID)
	if err != nil {
		return db.Snapshot{}, fmt.Errorf("найти исследование для снимка: %w", err)
	}
	if !run.RootNodeID.Valid {
		return db.Snapshot{}, errors.New("у исследования нет корня дерева")
	}
	ok, err := q.SnapshotRootExists(ctx, db.SnapshotRootExistsParams{ID: run.RootNodeID, RunID: runID})
	if err != nil {
		return db.Snapshot{}, fmt.Errorf("проверить корень дерева: %w", err)
	}
	if !ok {
		return db.Snapshot{}, errors.New("корень дерева не принадлежит исследованию или не является ролью")
	}
	tree, err := q.BuildSnapshotTree(ctx, db.BuildSnapshotTreeParams{RootNodeID: run.RootNodeID, RunID: runID})
	if err != nil {
		return db.Snapshot{}, fmt.Errorf("собрать дерево снимка: %w", err)
	}
	snapshot, err := q.CreateSnapshot(ctx, db.CreateSnapshotParams{RunID: runID, Tree: tree})
	if err != nil {
		return db.Snapshot{}, fmt.Errorf("сохранить снимок: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return db.Snapshot{}, fmt.Errorf("подтвердить создание снимка: %w", err)
	}
	return snapshot, nil
}
