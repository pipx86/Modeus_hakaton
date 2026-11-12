package api

import (
	"context"
	"errors"
	"os"
	"testing"

	"activity-descriptor/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPGStoreCRUD(t *testing.T) {
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая БД")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	p := NewPGStore(pool)
	source, err := p.CreateSource(domain.Source{Type: domain.SourceDocument, Title: "Проверка CRUD", Content: "Подтверждённый текст"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.CreateRun("Проверка CRUD")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, run.ID)
		pool.Exec(ctx, `DELETE FROM sources WHERE id=$1`, source.ID)
	}()
	uuid := func() string {
		var id string
		if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	nodeID, evidenceID := uuid(), uuid()
	nodes := []domain.Node{{ID: nodeID, Type: domain.NodeRole, Title: "Роль", Evidence: []domain.Evidence{{ID: evidenceID, SourceID: source.ID, Quote: "Подтверждённый текст"}}}}
	if err = p.SaveTree(ctx, run.ID, nodes); err != nil {
		t.Fatal(err)
	}
	title := "Изменённая роль"
	updated, err := p.UpdateNode(nodeID, NodePatch{Title: &title})
	if err != nil || updated.Title != title {
		t.Fatalf("%+v %v", updated, err)
	}
	if err = p.DeleteSource(source.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("используемый источник удалён: %v", err)
	}
	approved, skipped, err := p.BulkApproveEvidence([]string{evidenceID, evidenceID, "не UUID"})
	if err != nil || approved != 1 || skipped != 2 {
		t.Fatalf("%d %d %v", approved, skipped, err)
	}
	bad := []domain.Evidence{{ID: uuid(), SourceID: uuid(), Quote: "цитата"}}
	if _, err = p.UpdateNode(nodeID, NodePatch{Evidence: &bad}); err == nil {
		t.Fatal("несуществующий источник принят")
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM evidence WHERE node_id=$1 AND id=$2 AND approved`, nodeID, evidenceID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("подтверждение потеряно при откате: %d %v", count, err)
	}
	list, err := p.ListRuns()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range list {
		if r.ID == run.ID && r.Type == domain.RunTypeBatch {
			found = true
		}
	}
	if !found {
		t.Fatal("исследование отсутствует в списке")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if err = p.DeleteSource(source.ID); err != nil {
		t.Fatal(err)
	}
}
