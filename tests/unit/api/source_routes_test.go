package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"activity-descriptor/internal/api"
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v4"
)

func TestPostgresEvidenceAndSourceRoutes(t *testing.T) {
	pool, pg := testPostgresAPI(t)
	h := api.NewServer(pg).Routes()
	admin := testToken(t, auth.RoleAdmin)
	user := testToken(t, auth.RoleUser)
	when := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	src := func() *pgxmock.Rows {
		return pgxmock.NewRows([]string{"id", "type", "title", "content", "source_url", "created_at"}).
			AddRow(dbSource, domain.SourceStandard, "Документ", "Материал", "", &when)
	}
	pool.ExpectQuery("FROM sources s WHERE").WithArgs("transcript-test", false).WillReturnRows(src())
	resp := callAPI(h, http.MethodGet, "/api/v1/sources", user, nil)
	var sources []domain.Source
	if err := json.Unmarshal(resp.Body.Bytes(), &sources); resp.Code != 200 || err != nil ||
		len(sources) != 1 || sources[0].ID != dbSource || !sources[0].CreatedAt.Equal(when) {
		t.Fatalf("список источников: %d %s, %v", resp.Code, resp.Body.String(), err)
	}
	pool.ExpectQuery("FROM sources s WHERE s.id=").WithArgs("transcript-test", false, dbSource).WillReturnRows(src())
	resp = callAPI(h, http.MethodGet, "/api/v1/sources/"+dbSource, user, nil)
	var source domain.Source
	if err := json.Unmarshal(resp.Body.Bytes(), &source); resp.Code != 200 || err != nil || source.Content != "Материал" {
		t.Fatalf("источник: %d %s, %v", resp.Code, resp.Body.String(), err)
	}
	pool.ExpectQuery("FROM sources s WHERE s.id=").WithArgs("transcript-test", false, dbSource).WillReturnError(pgx.ErrNoRows)
	if resp = callAPI(h, http.MethodGet, "/api/v1/sources/"+dbSource, user, nil); resp.Code != 404 {
		t.Fatalf("отсутствующий источник: %d %s", resp.Code, resp.Body.String())
	}
	pool.ExpectQuery("FROM sources s WHERE").WithArgs("transcript-test", false).WillReturnError(errTestDB)
	if resp = callAPI(h, http.MethodGet, "/api/v1/sources", user, nil); resp.Code != 500 {
		t.Fatalf("ошибка выдачи источников: %d %s", resp.Code, resp.Body.String())
	}
	pool.ExpectQuery("FROM evidence WHERE").WithArgs(dbNode, dbSource).
		WillReturnRows(pgxmock.NewRows([]string{"id", "node", "source", "ref", "quote", "status"}).
			AddRow(dbEvidence, dbNode, dbSource, "r1", "Старая цитата", "approved"))
	resp = callAPI(h, http.MethodGet, "/api/v1/evidences?node_id="+dbNode+"&source_id="+dbSource, user, nil)
	var evidences []domain.Evidence
	if err := json.Unmarshal(resp.Body.Bytes(), &evidences); resp.Code != 200 || err != nil || len(evidences) != 1 ||
		evidences[0].Status != domain.EvidenceApproved || evidences[0].NodeID != dbNode {
		t.Fatalf("отфильтрованные доказательства: %d %s, %v", resp.Code, resp.Body.String(), err)
	}
	for _, path := range []string{"/api/v1/evidences?node_id=bad", "/api/v1/evidences?source_id=bad"} {
		if resp = callAPI(h, http.MethodGet, path, user, nil); resp.Code != 422 {
			t.Fatalf("некорректный фильтр: %d %s", resp.Code, resp.Body.String())
		}
	}
	createBody := []byte(`{"node_id":"` + dbNode + `","source_id":"` + dbSource + `","quote":"Новая цитата","ref_id":"r2"}`)
	if resp = callAPI(h, http.MethodPost, "/api/v1/evidences", user, createBody); resp.Code != 403 {
		t.Fatalf("обычный пользователь не должна менять доказательства: %d %s", resp.Code, resp.Body.String())
	}
	if resp = callAPI(h, http.MethodPost, "/api/v1/evidences", admin, []byte(`{"extra":true}`)); resp.Code != 400 {
		t.Fatalf("проверка контракта JSON: %d %s", resp.Code, resp.Body.String())
	}
	if resp = callAPI(h, http.MethodPost, "/api/v1/evidences", admin, []byte(`{"quote":"  "}`)); resp.Code != 422 {
		t.Fatalf("проверка полей цитаты: %d %s", resp.Code, resp.Body.String())
	}
	pool.ExpectQuery("INSERT INTO evidence").WithArgs(dbNode, dbSource, "Новая цитата", "r2").
		WillReturnRows(pgxmock.NewRows([]string{"id", "node", "source", "ref", "quote", "status"}).
			AddRow(dbEvidence, dbNode, dbSource, "r2", "Новая цитата", "pending"))
	resp = callAPI(h, http.MethodPost, "/api/v1/evidences", admin, createBody)
	var evidence domain.Evidence
	if err := json.Unmarshal(resp.Body.Bytes(), &evidence); resp.Code != 201 || err != nil ||
		evidence.Status != domain.EvidencePending || evidence.ID != dbEvidence {
		t.Fatalf("созданное подтверждение: %d %s, %v", resp.Code, resp.Body.String(), err)
	}
	pool.ExpectQuery("UPDATE evidence SET quote").WithArgs(dbEvidence, "Исправленная цитата").
		WillReturnRows(pgxmock.NewRows([]string{"id", "node", "source", "ref", "quote", "status"}).
			AddRow(dbEvidence, dbNode, dbSource, "r2", "Исправленная цитата", domain.EvidenceApproved))
	url := "/api/v1/evidences/" + dbEvidence
	resp = callAPI(h, http.MethodPatch, url, admin, []byte(`{"quote":" Исправленная цитата "}`))
	if err := json.Unmarshal(resp.Body.Bytes(), &evidence); resp.Code != 200 || err != nil ||
		evidence.Quote != "Исправленная цитата" || evidence.Status != domain.EvidenceApproved {
		t.Fatalf("исправленная цитата с прежним статусом: %d %s, %v", resp.Code, resp.Body.String(), err)
	}
	pool.ExpectQuery("FROM evidence WHERE id=").WithArgs(dbEvidence).
		WillReturnRows(pgxmock.NewRows([]string{"id", "node", "source", "ref", "quote", "status", "approved"}).
			AddRow(dbEvidence, dbNode, dbSource, "r2", "Исправленная цитата", "approved", false))
	resp = callAPI(h, http.MethodGet, url, user, nil)
	var details struct {
		domain.Evidence
		Approved bool `json:"approved"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &details); resp.Code != 200 || err != nil || !details.Approved || details.RefID != "r2" {
		t.Fatalf("состояние подтверждения: %d %s, %v", resp.Code, resp.Body.String(), err)
	}
}

func pgUUID(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPostgresAttachSourceCommitsBothWrites(t *testing.T) {
	ctx := context.Background()
	pool, pg := testPostgresAPI(t)
	runID, srcID := pgUUID(t, dbRun), pgUUID(t, dbSource)
	input := domain.Source{Type: domain.SourceStandard, Title: "Профстандарт", Content: "Текст"}
	when := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	for _, failed := range []bool{false, true} {
		pool.ExpectBegin()
		pool.ExpectQuery("SELECT EXISTS").WithArgs(runID).
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
		pool.ExpectQuery("INSERT INTO sources").WithArgs(string(input.Type), input.Title, input.Content, "", runID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "type", "title", "content", "url", "created_at"}).
				AddRow(srcID, input.Type, input.Title, input.Content, "", &when))
		link := pool.ExpectExec("INSERT INTO run_sources").WithArgs(runID, srcID)
		if failed {
			link.WillReturnError(errTestDB)
			pool.ExpectRollback()
		} else {
			link.WillReturnResult(pgxmock.NewResult("INSERT", 1))
			pool.ExpectCommit()
		}
		created, err := pg.AttachRunSource(ctx, dbRun, input)
		if failed {
			if created != nil || !errors.Is(err, errTestDB) {
				t.Fatalf("сбой привязки: %#v, %v", created, err)
			}
		} else if err != nil || created.ID != dbSource || created.Priority != domain.SourcePriority(input.Type) || !created.CreatedAt.Equal(when) {
			t.Fatalf("привязанный источник: %#v, %v", created, err)
		}
	}
	pool.ExpectBegin()
	pool.ExpectQuery("SELECT EXISTS").WithArgs(runID).WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	pool.ExpectRollback()
	if _, err := pg.AttachRunSource(ctx, dbRun, input); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("привязка к отсутствующему исследованию: %v", err)
	}
	pool.ExpectExec("INSERT INTO run_sources").WithArgs(runID, srcID).WillReturnResult(pgxmock.NewResult("INSERT", 0))
	if err := pg.LinkRunSources(ctx, dbRun, []string{"bad", dbSource}); err != nil {
		t.Fatalf("повторная привязка источника: %v", err)
	}
}

func TestPostgresSnapshotsHTTPKeepVersionTreeAndRunOwnership(t *testing.T) {
	pool, pg := testPostgresAPI(t)
	h := api.NewServer(pg).Routes()
	admin := testToken(t, auth.RoleAdmin)
	user := testToken(t, auth.RoleUser)
	url := "/api/v1/runs/" + dbRun + "/snapshots"
	snap := dbEvidence
	root := dbNode
	runID, rootID, snapID := pgUUID(t, dbRun), pgUUID(t, root), pgUUID(t, snap)
	tree := []byte(`{"root_node_id":"` + root + `","nodes":[]}`)
	stamp := pgtype.Timestamptz{Time: time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC), Valid: true}
	row := func() *pgxmock.Rows {
		return pgxmock.NewRows([]string{"id", "run_id", "version", "tree", "created_at"}).
			AddRow(snapID, runID, int32(2), tree, stamp.Time)
	}
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(dbRun).WillReturnRows(runRow())
	resp := callAPI(h, http.MethodPost, url, admin, nil)
	if resp.Code != 409 {
		t.Fatalf("снимок до сохранения дерева: %d %s", resp.Code, resp.Body.String())
	}
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(dbRun).
		WillReturnRows(pgxmock.NewRows([]string{"id", "profession", "status", "stage", "type", "created_by", "root", "started", "finished"}).
			AddRow(dbRun, "Инженер", domain.RunDone, domain.StageDone, domain.RunTypeBatch, "", &root, nil, nil))
	pool.ExpectBegin()
	pool.ExpectQuery("SELECT id, root_node_id FROM runs").WithArgs(runID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "root"}).AddRow(runID, rootID))
	pool.ExpectQuery("SELECT EXISTS").WithArgs(rootID, runID).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	pool.ExpectQuery("SELECT jsonb_build_object").WithArgs(rootID, runID).
		WillReturnRows(pgxmock.NewRows([]string{"tree"}).AddRow(tree))
	pool.ExpectQuery("INSERT INTO snapshots").WithArgs(runID, tree).WillReturnRows(row())
	pool.ExpectCommit()
	resp = callAPI(h, http.MethodPost, url, admin, nil)
	type snapshot struct {
		ID      string          `json:"id"`
		RunID   string          `json:"run_id"`
		Version int             `json:"version"`
		Tree    json.RawMessage `json:"tree"`
	}
	var created snapshot
	if err := json.Unmarshal(resp.Body.Bytes(), &created); resp.Code != 201 || err != nil ||
		created.ID != snap || created.RunID != dbRun || created.Version != 2 || string(created.Tree) != string(tree) {
		t.Fatalf("созданный снимок: %d %s, %v", resp.Code, resp.Body.String(), err)
	}
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(dbRun).WillReturnRows(runRow())
	pool.ExpectQuery("ListSnapshotsByRun").WithArgs(runID).WillReturnRows(row())
	resp = callAPI(h, http.MethodGet, url, user, nil)
	var versions []snapshot
	if err := json.Unmarshal(resp.Body.Bytes(), &versions); resp.Code != 200 || err != nil || len(versions) != 1 || versions[0].Version != 2 {
		t.Fatalf("список версий: %d %s, %v", resp.Code, resp.Body.String(), err)
	}
	pool.ExpectQuery("GetSnapshot").WithArgs(runID, snapID).WillReturnRows(row())
	resp = callAPI(h, http.MethodGet, url+"/"+snap, user, nil)
	var selected snapshot
	if err := json.Unmarshal(resp.Body.Bytes(), &selected); resp.Code != 200 || err != nil ||
		selected.ID != created.ID || string(selected.Tree) != string(tree) {
		t.Fatalf("выбранный снимок: %d %s, %v", resp.Code, resp.Body.String(), err)
	}
	pool.ExpectQuery("GetSnapshot").WithArgs(runID, snapID).WillReturnError(pgx.ErrNoRows)
	if resp = callAPI(h, http.MethodGet, url+"/"+snap, user, nil); resp.Code != 404 {
		t.Fatalf("недоступный в исследовании снимок: %d %s", resp.Code, resp.Body.String())
	}
}
