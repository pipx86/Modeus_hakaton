package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
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
	source, err := p.CreateSource(domain.Source{Type: domain.SourceDocument, Title: "Проверка CRUD", Content: "Подтверждённый текст", SourceURL: "https://example.org/source"})
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
	handler := NewServer(p).Routes()
	createdSource := do(t, handler, "POST", "/api/v1/sources", "teacher", `{"type":"document","title":"Источник API","source_url":"https://example.org/api"}`)
	var apiSource domain.Source
	if createdSource.Code != 201 || json.Unmarshal(createdSource.Body.Bytes(), &apiSource) != nil || apiSource.SourceURL != "https://example.org/api" {
		t.Fatalf("создание источника: %d %s", createdSource.Code, createdSource.Body.String())
	}
	defer p.DeleteSource(apiSource.ID)
	sourceResponse := do(t, handler, "GET", "/api/v1/sources/"+source.ID, "teacher", "")
	if sourceResponse.Code != 200 || !strings.Contains(sourceResponse.Body.String(), "Проверка CRUD") || !strings.Contains(sourceResponse.Body.String(), "https://example.org/source") {
		t.Fatalf("источник: %d %s", sourceResponse.Code, sourceResponse.Body.String())
	}
	sources := do(t, handler, "GET", "/api/v1/sources", "teacher", "")
	if sources.Code != 200 || !strings.Contains(sources.Body.String(), source.ID) {
		t.Fatalf("список источников: %d %s", sources.Code, sources.Body.String())
	}
	added := do(t, handler, "POST", "/api/v1/evidences", "teacher", fmt.Sprintf(`{"node_id":%q,"source_id":%q,"ref_id":"строка-7","quote":"Подтверждённый текст"}`, nodeID, source.ID))
	if added.Code != 201 {
		t.Fatalf("добавление подтверждения: %d %s", added.Code, added.Body.String())
	}
	var addedEvidence domain.Evidence
	if err = json.Unmarshal(added.Body.Bytes(), &addedEvidence); err != nil || addedEvidence.ID == "" || addedEvidence.NodeID != nodeID || addedEvidence.RefID != "строка-7" {
		t.Fatalf("%s %v", added.Body.String(), err)
	}
	listed := do(t, handler, "GET", "/api/v1/evidences?node_id="+nodeID, "teacher", "")
	if listed.Code != 200 || !strings.Contains(listed.Body.String(), addedEvidence.ID) || !strings.Contains(listed.Body.String(), nodeID) {
		t.Fatalf("подтверждения: %d %s", listed.Code, listed.Body.String())
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
	unchanged := []domain.Evidence{{ID: evidenceID, SourceID: source.ID, Quote: "Подтверждённый текст"}, addedEvidence}
	if _, err = p.UpdateNode(nodeID, NodePatch{Evidence: &unchanged}); err != nil {
		t.Fatalf("не удалось сохранить подтверждения: %v", err)
	}
	var stillApproved bool
	if err = pool.QueryRow(ctx, `SELECT approved FROM evidence WHERE id=$1`, evidenceID).Scan(&stillApproved); err != nil || !stillApproved {
		t.Fatalf("статус неизменённого подтверждения потерян: %v, %v", stillApproved, err)
	}
	unchanged[0].Quote = "Исправленная цитата"
	if _, err = p.UpdateNode(nodeID, NodePatch{Evidence: &unchanged}); err != nil {
		t.Fatalf("не удалось исправить цитату: %v", err)
	}
	if err = pool.QueryRow(ctx, `SELECT approved FROM evidence WHERE id=$1`, evidenceID).Scan(&stillApproved); err != nil || stillApproved {
		t.Fatalf("изменённая цитата осталась подтверждённой: %v, %v", stillApproved, err)
	}
	if err = p.ApproveEvidence(evidenceID); err != nil {
		t.Fatal(err)
	}
	duplicates := []domain.Evidence{unchanged[0], unchanged[0]}
	if _, err = p.UpdateNode(nodeID, NodePatch{Evidence: &duplicates}); err == nil {
		t.Fatal("повторяющийся идентификатор подтверждения принят")
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
