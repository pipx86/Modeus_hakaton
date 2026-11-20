package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v4"
)

func testUUID(n int) pgtype.UUID {
	var id pgtype.UUID
	if err := id.Scan(fmt.Sprintf("00000000-0000-4000-8000-%012d", n)); err != nil {
		panic(err)
	}
	return id
}

func testTree() store.TreeNode {
	return store.TreeNode{
		ID: testUUID(1), Title: "Роль",
		Evidence: []store.TreeEvidence{{ID: testUUID(101), SourceID: testUUID(901), Quote: "Цитата"}},
		Children: []store.TreeNode{{
			ID: testUUID(2), Title: "Функция",
			Evidence: []store.TreeEvidence{{ID: testUUID(102), SourceID: testUUID(901), Quote: "Ещё цитата"}},
		}},
	}
}

func testStore(t *testing.T) (pgxmock.PgxPoolIface, *store.Store) {
	t.Helper()
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, store.New(pool)
}

func anyArgs(count int) []any {
	args := make([]any, count)
	for i := range args {
		args[i] = pgxmock.AnyArg()
	}
	return args
}

func expectNode(pool pgxmock.PgxPoolIface, id, runID, parentID pgtype.UUID, level int16, kind, title string) {
	pool.ExpectQuery("INSERT INTO nodes").WithArgs(anyArgs(7)...).WillReturnRows(pgxmock.NewRows([]string{
		"id", "run_id", "parent_id", "level", "type", "title", "description", "created_at", "updated_at", "zun", "review_status", "review_comment", "reviewed_at", "reviewed_by",
	}).AddRow(id, runID, parentID, level, kind, title, pgtype.Text{}, time.Now(), time.Now(), []byte(nil), "pending", "", nil, ""))
}

func expectEvidence(pool pgxmock.PgxPoolIface, e store.TreeEvidence, nodeID, runID pgtype.UUID) {
	pool.ExpectQuery("INSERT INTO evidence").WithArgs(anyArgs(6)...).WillReturnRows(pgxmock.NewRows([]string{
		"id", "node_id", "source_id", "quote", "approved", "created_at", "run_id", "ref_id", "status",
	}).AddRow(e.ID, nodeID, e.SourceID, e.Quote, e.Approved, time.Now(), runID, e.RefID, "pending"))
}

func TestSaveTreeUnit(t *testing.T) {
	t.Run("без идентификатора исследования", func(t *testing.T) {
		pool, s := testStore(t)
		if err := s.SaveTree(context.Background(), pgtype.UUID{}, testTree()); err == nil {
			t.Fatal("ожидалась ошибка")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("невалидное дерево не открывает транзакцию", func(t *testing.T) {
		pool, s := testStore(t)
		root := testTree()
		root.Title = "  "
		if err := s.SaveTree(context.Background(), testUUID(500), root); err == nil {
			t.Fatal("ожидалась ошибка проверки дерева")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("ошибка начала транзакции", func(t *testing.T) {
		pool, s := testStore(t)
		pool.ExpectBegin().WillReturnError(errors.New("сбой"))
		if err := s.SaveTree(context.Background(), testUUID(500), testTree()); err == nil {
			t.Fatal("ожидалась ошибка")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("успешно сохраняет дерево целиком", func(t *testing.T) {
		pool, s := testStore(t)
		runID := testUUID(500)
		root := testTree()
		pool.ExpectBegin()
		expectNode(pool, root.ID, runID, pgtype.UUID{}, 0, "role", root.Title)
		expectEvidence(pool, root.Evidence[0], root.ID, runID)
		child := root.Children[0]
		expectNode(pool, child.ID, runID, root.ID, 1, "function", child.Title)
		expectEvidence(pool, child.Evidence[0], child.ID, runID)
		pool.ExpectExec("UPDATE runs SET root_node_id").WithArgs(anyArgs(2)...).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		pool.ExpectCommit()
		if err := s.SaveTree(context.Background(), runID, root); err != nil {
			t.Fatal(err)
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("ошибка записи узла откатывает изменения", func(t *testing.T) {
		pool, s := testStore(t)
		pool.ExpectBegin()
		pool.ExpectQuery("INSERT INTO nodes").WithArgs(anyArgs(7)...).WillReturnError(errors.New("сбой узла"))
		pool.ExpectRollback()
		if err := s.SaveTree(context.Background(), testUUID(500), testTree()); err == nil {
			t.Fatal("ожидалась ошибка")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("ошибка записи подтверждения откатывает дерево", func(t *testing.T) {
		pool, s := testStore(t)
		pool.ExpectBegin()
		runID := testUUID(500)
		root := testTree()
		expectNode(pool, root.ID, runID, pgtype.UUID{}, 0, "role", root.Title)
		pool.ExpectQuery("INSERT INTO evidence").WithArgs(anyArgs(6)...).WillReturnError(errors.New("сбой подтверждения"))
		pool.ExpectRollback()
		if err := s.SaveTree(context.Background(), runID, root); err == nil {
			t.Fatal("ожидалась ошибка")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("корень нельзя назначить повторно", func(t *testing.T) {
		pool, s := testStore(t)
		runID := testUUID(500)
		root := testTree()
		root.Children = nil
		pool.ExpectBegin()
		expectNode(pool, root.ID, runID, pgtype.UUID{}, 0, "role", root.Title)
		expectEvidence(pool, root.Evidence[0], root.ID, runID)
		pool.ExpectExec("UPDATE runs SET root_node_id").WithArgs(anyArgs(2)...).WillReturnResult(pgxmock.NewResult("UPDATE", 0))
		pool.ExpectRollback()
		if err := s.SaveTree(context.Background(), runID, root); err == nil {
			t.Fatal("ожидалась ошибка существующего корня")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("ошибка назначения корня откатывает изменения", func(t *testing.T) {
		pool, s := testStore(t)
		runID := testUUID(500)
		root := testTree()
		root.Children = nil
		pool.ExpectBegin()
		expectNode(pool, root.ID, runID, pgtype.UUID{}, 0, "role", root.Title)
		expectEvidence(pool, root.Evidence[0], root.ID, runID)
		pool.ExpectExec("UPDATE runs SET root_node_id").WithArgs(anyArgs(2)...).WillReturnError(errors.New("сбой"))
		pool.ExpectRollback()
		if err := s.SaveTree(context.Background(), runID, root); err == nil {
			t.Fatal("ожидалась ошибка")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("ошибка подтверждения транзакции возвращается", func(t *testing.T) {
		pool, s := testStore(t)
		runID := testUUID(500)
		root := testTree()
		pool.ExpectBegin()
		expectNode(pool, root.ID, runID, pgtype.UUID{}, 0, "role", root.Title)
		expectEvidence(pool, root.Evidence[0], root.ID, runID)
		expectNode(pool, root.Children[0].ID, runID, root.ID, 1, "function", root.Children[0].Title)
		expectEvidence(pool, root.Children[0].Evidence[0], root.Children[0].ID, runID)
		pool.ExpectExec("UPDATE runs SET root_node_id").WithArgs(anyArgs(2)...).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		pool.ExpectCommit().WillReturnError(errors.New("сбой фиксации"))
		pool.ExpectRollback()
		if err := s.SaveTree(context.Background(), runID, root); err == nil {
			t.Fatal("ожидалась ошибка фиксации")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSaveSnapshotUnit(t *testing.T) {
	t.Run("без идентификатора", func(t *testing.T) {
		pool, s := testStore(t)
		if _, err := s.SaveSnapshot(context.Background(), pgtype.UUID{}); err == nil {
			t.Fatal("ожидалась ошибка")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("ошибка начала транзакции", func(t *testing.T) {
		pool, s := testStore(t)
		pool.ExpectBegin().WillReturnError(errors.New("сбой"))
		if _, err := s.SaveSnapshot(context.Background(), testUUID(500)); err == nil {
			t.Fatal("ожидалась ошибка")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("исследование не найдено", func(t *testing.T) {
		pool, s := testStore(t)
		pool.ExpectBegin()
		pool.ExpectQuery("SELECT id, root_node_id FROM runs").WithArgs(anyArgs(1)...).WillReturnError(errors.New("не найдено"))
		pool.ExpectRollback()
		if _, err := s.SaveSnapshot(context.Background(), testUUID(500)); err == nil {
			t.Fatal("ожидалась ошибка")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("дерево без корня", func(t *testing.T) {
		pool, s := testStore(t)
		pool.ExpectBegin()
		pool.ExpectQuery("SELECT id, root_node_id FROM runs").WithArgs(anyArgs(1)...).WillReturnRows(pgxmock.NewRows([]string{"id", "root_node_id"}).AddRow(testUUID(500), pgtype.UUID{}))
		pool.ExpectRollback()
		if _, err := s.SaveSnapshot(context.Background(), testUUID(500)); err == nil {
			t.Fatal("ожидалась ошибка")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("корень не принадлежит исследованию", func(t *testing.T) {
		pool, s := testStore(t)
		pool.ExpectBegin()
		pool.ExpectQuery("SELECT id, root_node_id FROM runs").WithArgs(anyArgs(1)...).WillReturnRows(pgxmock.NewRows([]string{"id", "root_node_id"}).AddRow(testUUID(500), testUUID(1)))
		pool.ExpectQuery("SELECT EXISTS").WithArgs(anyArgs(2)...).WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
		pool.ExpectRollback()
		if _, err := s.SaveSnapshot(context.Background(), testUUID(500)); err == nil {
			t.Fatal("ожидалась ошибка")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("сохраняет дерево со снимком", func(t *testing.T) {
		pool, s := testStore(t)
		runID, rootID, snapshotID := testUUID(500), testUUID(1), testUUID(700)
		tree := []byte(`{"root_node_id":"root"}`)
		pool.ExpectBegin()
		pool.ExpectQuery("SELECT id, root_node_id FROM runs").WithArgs(anyArgs(1)...).WillReturnRows(pgxmock.NewRows([]string{"id", "root_node_id"}).AddRow(runID, rootID))
		pool.ExpectQuery("SELECT EXISTS").WithArgs(anyArgs(2)...).WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
		pool.ExpectQuery("SELECT jsonb_build_object").WithArgs(anyArgs(2)...).WillReturnRows(pgxmock.NewRows([]string{"tree"}).AddRow(tree))
		pool.ExpectQuery("INSERT INTO snapshots").WithArgs(anyArgs(2)...).WillReturnRows(pgxmock.NewRows([]string{"id", "run_id", "version", "tree", "created_at"}).AddRow(snapshotID, runID, int32(1), tree, time.Now()))
		pool.ExpectCommit()
		snapshot, err := s.SaveSnapshot(context.Background(), runID)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.ID != snapshotID || snapshot.Version != 1 {
			t.Fatalf("неверный снимок: %+v", snapshot)
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRunStatusSaveUnit(t *testing.T) {
	validRun := &domain.Run{ID: "00000000-0000-4000-8000-000000000500", Status: domain.RunDone}
	cases := []struct {
		name  string
		store store.RunStatusStore
		run   *domain.Run
	}{
		{"нет хранилища", store.RunStatusStore{Timeout: time.Second}, validRun},
		{"нет исследования", store.RunStatusStore{Store: store.New(nil), Timeout: time.Second}, nil},
		{"нет тайм-аута", store.RunStatusStore{Store: store.New(nil)}, validRun},
		{"неверный UUID", store.RunStatusStore{Store: store.New(nil), Timeout: time.Second}, &domain.Run{ID: "не uuid", Status: domain.RunDone}},
		{"неизвестный статус", store.RunStatusStore{Store: store.New(nil), Timeout: time.Second}, &domain.Run{ID: validRun.ID, Status: "unknown"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.store.Save(tc.run); err == nil {
				t.Fatal("ожидалась ошибка")
			}
		})
	}
	t.Run("сохраняет статус", func(t *testing.T) {
		pool, s := testStore(t)
		pool.ExpectExec("UPDATE runs SET status").WithArgs(anyArgs(2)...).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		if err := (store.RunStatusStore{Store: s, Timeout: time.Second}).Save(validRun); err != nil {
			t.Fatal(err)
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("ошибка базы возвращается", func(t *testing.T) {
		pool, s := testStore(t)
		pool.ExpectExec("UPDATE runs SET status").WithArgs(anyArgs(2)...).WillReturnError(errors.New("сбой"))
		if err := (store.RunStatusStore{Store: s, Timeout: time.Second}).Save(validRun); err == nil {
			t.Fatal("ожидалась ошибка базы")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("отсутствующее исследование", func(t *testing.T) {
		pool, s := testStore(t)
		pool.ExpectExec("UPDATE runs SET status").WithArgs(anyArgs(2)...).WillReturnResult(pgxmock.NewResult("UPDATE", 0))
		if err := (store.RunStatusStore{Store: s, Timeout: time.Second}).Save(validRun); err == nil {
			t.Fatal("ожидалась ошибка отсутствующего исследования")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSnapshotRollsBackWhenAnyStepFailsAfterLock(t *testing.T) {
	runID, rootID, snapshotID := testUUID(500), testUUID(1), testUUID(700)
	tree := []byte(`{"root_node_id":"root"}`)
	dbErr := errors.New("сбой записи снимка")
	for _, step := range []string{"проверка корня", "построение дерева", "запись снимка", "фиксация"} {
		t.Run(step, func(t *testing.T) {
			pool, repo := testStore(t)
			pool.ExpectBegin()
			pool.ExpectQuery("SELECT id, root_node_id FROM runs").WithArgs(runID).
				WillReturnRows(pgxmock.NewRows([]string{"id", "root_node_id"}).AddRow(runID, rootID))
			verify := pool.ExpectQuery("SELECT EXISTS").WithArgs(rootID, runID)
			if step == "проверка корня" {
				verify.WillReturnError(dbErr)
			} else {
				verify.WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
				build := pool.ExpectQuery("SELECT jsonb_build_object").WithArgs(rootID, runID)
				if step == "построение дерева" {
					build.WillReturnError(dbErr)
				} else {
					build.WillReturnRows(pgxmock.NewRows([]string{"tree"}).AddRow(tree))
					create := pool.ExpectQuery("INSERT INTO snapshots").WithArgs(runID, tree)
					if step == "запись снимка" {
						create.WillReturnError(dbErr)
					} else {
						create.WillReturnRows(pgxmock.NewRows([]string{"id", "run_id", "version", "tree", "created_at"}).
							AddRow(snapshotID, runID, int32(1), tree, time.Now()))
						pool.ExpectCommit().WillReturnError(dbErr)
					}
				}
			}
			pool.ExpectRollback()
			got, err := repo.SaveSnapshot(context.Background(), runID)
			if !errors.Is(err, dbErr) || got.ID.Valid {
				t.Fatalf("после ошибки %s получен снимок: %+v, %v", step, got, err)
			}
			if err := pool.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
