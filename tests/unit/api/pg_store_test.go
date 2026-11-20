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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v4"
)

const (
	dbRun         = "805cd798-35d0-4637-ab57-00d9cd706624"
	dbNode        = "805cd798-35d0-4637-ab57-00d9cd706625"
	dbSource      = "805cd798-35d0-4637-ab57-00d9cd706626"
	dbEvidence    = "805cd798-35d0-4637-ab57-00d9cd706627"
	dbEvidenceTwo = "805cd798-35d0-4637-ab57-00d9cd706628"
)

var errTestDB = errors.New("test database unavailable")

func testPostgresAPI(t *testing.T) (pgxmock.PgxPoolIface, *api.PGStore) {
	t.Helper()
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer pool.Close()
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})
	return pool, api.NewPGStore(pool)
}

func runRow() *pgxmock.Rows {
	return pgxmock.NewRows([]string{"id", "profession", "status", "stage", "type", "created_by", "root", "started", "finished", "result_kind", "program_id"}).
		AddRow(dbRun, "Инженер", domain.RunRunning, domain.StageAnalysis, domain.RunTypeBatch, "", nil, nil, nil, "", "")
}

func TestPostgresRunReadThroughHTTP(t *testing.T) {
	pool, state := testPostgresAPI(t)
	h := api.NewServer(state).Routes()
	token := testToken(t, auth.RoleAdmin)
	url := "/api/v1/runs/" + dbRun
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(dbRun).WillReturnRows(runRow())
	resp := callAPI(h, http.MethodGet, url, token, nil)
	var run domain.Run
	if err := json.Unmarshal(resp.Body.Bytes(), &run); resp.Code != http.StatusOK || err != nil ||
		run.ID != dbRun || run.Status != domain.RunRunning || run.Stage != domain.StageAnalysis || run.Type != domain.RunTypeBatch {
		t.Fatalf("исследование: %d %s, %v", resp.Code, resp.Body.String(), err)
	}
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(dbRun).WillReturnError(pgx.ErrNoRows)
	if resp = callAPI(h, http.MethodGet, url, token, nil); resp.Code != http.StatusNotFound {
		t.Fatalf("отсутствующее исследование: %d %s", resp.Code, resp.Body.String())
	}
	if resp = callAPI(h, http.MethodGet, "/api/v1/runs/bad-id", token, nil); resp.Code != http.StatusNotFound {
		t.Fatalf("некорректный идентификатор: %d %s", resp.Code, resp.Body.String())
	}
}

func TestPostgresCreateRunAndConstraintErrors(t *testing.T) {
	pool, state := testPostgresAPI(t)
	pool.ExpectQuery("INSERT INTO runs").WithArgs("Инженер").
		WillReturnRows(pgxmock.NewRows([]string{"id", "profession", "status", "stage", "type", "created_by"}).
			AddRow(dbRun, "Инженер", domain.RunPending, domain.StageCreated, domain.RunTypeBatch, ""))
	run, err := state.CreateRun(" Инженер ")
	if err != nil || run.ID != dbRun || run.Profession != "Инженер" || run.Status != domain.RunPending {
		t.Fatalf("созданное исследование: %#v, %v", run, err)
	}
	expectTreeError(t, createRunError(state, " \t"), "EMPTY_PROFESSION")
	for _, tc := range []struct {
		err  error
		code string
	}{
		{&pgconn.PgError{ConstraintName: "EMPTY_TITLE"}, "EMPTY_TITLE"},
		{&pgconn.PgError{Code: "23505"}, "DUPLICATE_ID"},
		{&pgconn.PgError{Code: "23503"}, "INVALID_REFERENCE"},
	} {
		pool.ExpectQuery("INSERT INTO runs").WithArgs("Инженер").WillReturnError(tc.err)
		expectTreeError(t, createRunError(state, "Инженер"), tc.code)
	}
	pool.ExpectQuery("INSERT INTO runs").WithArgs("Инженер").WillReturnError(errTestDB)
	if err := createRunError(state, "Инженер"); !errors.Is(err, errTestDB) {
		t.Fatalf("ошибка запроса потеряна: %v", err)
	}
}

func createRunError(state *api.PGStore, profession string) error {
	_, err := state.CreateRun(profession)
	return err
}

func expectTreeError(t *testing.T, err error, code string) {
	t.Helper()
	var validation *domain.TreeValidationError
	if !errors.As(err, &validation) || validation.Code != code {
		t.Fatalf("ожидалась ошибка %s, получено %v", code, err)
	}
}

func TestPostgresTreeAndAnswerMapping(t *testing.T) {
	pool, state := testPostgresAPI(t)
	expectRun := func() {
		pool.ExpectQuery("FROM runs WHERE id=").WithArgs(dbRun).WillReturnRows(runRow())
	}
	treeRows := func(data []byte) *pgxmock.Rows {
		return pgxmock.NewRows([]string{"id", "type", "title", "description", "parent", "evidence", "zun", "review_status", "review_comment", "reviewed_at"}).
			AddRow(dbNode, domain.NodeType("role"), "Инженер", "Описание", nil, data, []byte(`{"knowledge":["Диагностика"]}`), "pending", "", nil)
	}
	citation, err := json.Marshal([]domain.Evidence{{ID: dbEvidence, SourceID: dbSource, Quote: "Цитата"}})
	if err != nil {
		t.Fatal(err)
	}
	expectRun()
	pool.ExpectQuery("FROM nodes n WHERE n.run_id").WithArgs(dbRun).WillReturnRows(treeRows(citation))
	tree, err := state.GetTree(dbRun)
	if err != nil || len(tree) != 1 || tree[0].ID != dbNode || tree[0].Zun == nil || len(tree[0].Zun.Knowledge) != 1 ||
		len(tree[0].Evidence) != 1 || tree[0].Evidence[0].SourceID != dbSource {
		t.Fatalf("дерево: %#v, %v", tree, err)
	}
	expectRun()
	pool.ExpectQuery("FROM nodes n WHERE n.run_id").WithArgs(dbRun).WillReturnRows(treeRows([]byte("[")))
	if _, err := state.GetTree(dbRun); err == nil {
		t.Fatal("ошибка разбора JSON подтверждений потеряна")
	}
	expectRun()
	pool.ExpectQuery("FROM evidence e JOIN nodes").WithArgs(dbRun).
		WillReturnRows(pgxmock.NewRows([]string{"id", "node", "source", "ref", "quote", "status", "claim_origin", "interview_id"}).
			AddRow(dbEvidence, dbNode, dbSource, "r1", "Цитата", "rejected", "source_statement", ""))
	answers, err := state.GetAnswers(dbRun)
	if err != nil || len(answers) != 1 || answers[0].Status != domain.EvidenceRejected || answers[0].RefID != "r1" {
		t.Fatalf("подтверждения: %#v, %v", answers, err)
	}
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(dbRun).WillReturnError(pgx.ErrNoRows)
	if _, err := state.GetAnswers(dbRun); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("ответы отсутствующего исследования: %v", err)
	}
	expectRun()
	pool.ExpectQuery("FROM evidence e JOIN nodes").WithArgs(dbRun).WillReturnError(errTestDB)
	if _, err := state.GetAnswers(dbRun); !errors.Is(err, errTestDB) {
		t.Fatalf("ошибка выдачи ответов: %v", err)
	}
}

func TestPostgresRunUpdatesAndDeletion(t *testing.T) {
	pool, pg := testPostgresAPI(t)
	for _, tc := range []struct {
		name, sql string
		args      []any
		call      func(string) error
	}{
		{"этап", "UPDATE runs SET stage", []any{dbRun, string(domain.StageAnalysis)}, func(id string) error { return pg.SetStage(id, domain.StageAnalysis) }},
		{"статус", "UPDATE runs SET status", []any{dbRun, string(domain.RunFailed)}, func(id string) error { return pg.SetStatus(id, domain.RunFailed) }},
		{"тип", "UPDATE runs SET type", []any{dbRun, string(domain.RunTypeBatch)}, func(id string) error { return pg.SetRunType(id, domain.RunTypeBatch) }},
		{"сохранение состояния", "UPDATE runs SET status", []any{dbRun, string(domain.RunRunning), string(domain.StageAnalysis)}, func(id string) error {
			return pg.Save(&domain.Run{ID: id, Status: domain.RunRunning, Stage: domain.StageAnalysis})
		}},
		{"удаление исследования", "DELETE FROM runs", []any{dbRun}, pg.DeleteRun},
		{"удаление источника", "DELETE FROM sources", []any{dbRun}, pg.DeleteSource},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call("bad-id"); !errors.Is(err, api.ErrNotFound) {
				t.Fatalf("проверка идентификатора: %v", err)
			}
			pool.ExpectExec(tc.sql).WithArgs(tc.args...).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
			if err := tc.call(dbRun); err != nil {
				t.Fatalf("изменение одной записи: %v", err)
			}
			pool.ExpectExec(tc.sql).WithArgs(tc.args...).WillReturnResult(pgxmock.NewResult("UPDATE", 0))
			if err := tc.call(dbRun); !errors.Is(err, api.ErrNotFound) {
				t.Fatalf("отсутствующая запись: %v", err)
			}
			pool.ExpectExec(tc.sql).WithArgs(tc.args...).WillReturnError(errTestDB)
			if err := tc.call(dbRun); !errors.Is(err, errTestDB) {
				t.Fatalf("ошибка БД: %v", err)
			}
		})
	}
	if err := pg.SetRunType(dbRun, domain.RunType("unknown")); err == nil {
		t.Fatal("неизвестный тип исследования записан")
	}
	pool.ExpectExec("DELETE FROM sources").WithArgs(dbSource).WillReturnError(&pgconn.PgError{Code: "23503"})
	if err := pg.DeleteSource(dbSource); !errors.Is(err, api.ErrConflict) {
		t.Fatalf("привязанный источник: %v", err)
	}
}

func TestPostgresEvidenceReviewTransitions(t *testing.T) {
	pool, state := testPostgresAPI(t)
	for _, tc := range []struct {
		target string
		run    func(string) error
	}{
		{"approved", state.ApproveEvidence},
		{"rejected", state.RejectEvidence},
	} {
		t.Run(tc.target, func(t *testing.T) {
			update := "UPDATE evidence SET status='" + tc.target + "'"
			if err := tc.run("bad-id"); !errors.Is(err, api.ErrNotFound) {
				t.Fatalf("некорректный идентификатор: %v", err)
			}
			pool.ExpectExec(update).WithArgs(dbEvidence).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
			if err := tc.run(dbEvidence); err != nil {
				t.Fatalf("изменение статуса: %v", err)
			}
			for _, outcome := range []struct {
				status string
				err    error
			}{
				{"approved", api.ErrAlreadyApproved}, {"rejected", api.ErrAlreadyRejected},
			} {
				pool.ExpectExec(update).WithArgs(dbEvidence).WillReturnResult(pgxmock.NewResult("UPDATE", 0))
				pool.ExpectQuery("FROM evidence WHERE id").WithArgs(dbEvidence).
					WillReturnRows(pgxmock.NewRows([]string{"status"}).AddRow(outcome.status))
				if err := tc.run(dbEvidence); !errors.Is(err, outcome.err) {
					t.Fatalf("предыдущий статус %s: %v", outcome.status, err)
				}
			}
			pool.ExpectExec(update).WithArgs(dbEvidence).WillReturnResult(pgxmock.NewResult("UPDATE", 0))
			pool.ExpectQuery("FROM evidence WHERE id").WithArgs(dbEvidence).WillReturnError(pgx.ErrNoRows)
			if err := tc.run(dbEvidence); !errors.Is(err, api.ErrNotFound) {
				t.Fatalf("отсутствующая запись: %v", err)
			}
			pool.ExpectExec(update).WithArgs(dbEvidence).WillReturnError(errTestDB)
			if err := tc.run(dbEvidence); !errors.Is(err, errTestDB) {
				t.Fatalf("ошибка обновления: %v", err)
			}
		})
	}
}

func TestPostgresBulkApproveCommitsOrRollsBack(t *testing.T) {
	pool, pg := testPostgresAPI(t)
	ids := []string{dbEvidence, "bad-id", dbEvidenceTwo}
	pool.ExpectBegin()
	pool.ExpectExec("UPDATE evidence").WithArgs(dbEvidence).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	pool.ExpectExec("UPDATE evidence").WithArgs(dbEvidenceTwo).WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	pool.ExpectCommit()
	ok, skipped, err := pg.BulkApproveEvidence(ids)
	if err != nil || ok != 1 || skipped != 2 {
		t.Fatalf("пакет: %d, %d, %v", ok, skipped, err)
	}
	pool.ExpectBegin()
	pool.ExpectExec("UPDATE evidence").WithArgs(dbEvidence).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	pool.ExpectExec("UPDATE evidence").WithArgs(dbEvidenceTwo).WillReturnError(errTestDB)
	pool.ExpectRollback()
	ok, skipped, err = pg.BulkApproveEvidence(ids)
	if !errors.Is(err, errTestDB) || ok != 0 || skipped != 0 {
		t.Fatalf("частичное выполнение отменено: %d, %d, %v", ok, skipped, err)
	}
	pool.ExpectBegin().WillReturnError(errTestDB)
	if _, _, err := pg.BulkApproveEvidence(ids); !errors.Is(err, errTestDB) {
		t.Fatalf("открытие транзакции: %v", err)
	}
	pool.ExpectBegin()
	pool.ExpectCommit().WillReturnError(errTestDB)
	pool.ExpectRollback()
	if _, _, err := pg.BulkApproveEvidence(nil); !errors.Is(err, errTestDB) {
		t.Fatalf("фиксация транзакции: %v", err)
	}
}

func TestPostgresUpdateNodeReplacesEvidenceAtomically(t *testing.T) {
	pool, pg := testPostgresAPI(t)
	title := "Уточнённая роль"
	e := domain.Evidence{ID: dbEvidence, SourceID: dbSource, Quote: "Точная цитата", RefID: "r2"}
	patch := api.NodePatch{Title: &title, Evidence: &[]domain.Evidence{e}}
	payload, err := json.Marshal(*patch.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{"", "conflict", "delete", "commit"} {
		t.Run("транзакция/"+failure, func(t *testing.T) {
			pool.ExpectBegin()
			pool.ExpectQuery("UPDATE nodes SET title").WithArgs(dbNode, patch.Title, patch.Description).
				WillReturnRows(pgxmock.NewRows([]string{"id", "type", "title", "description", "parent"}).
					AddRow(dbNode, domain.NodeType("role"), title, "", nil))
			insert := pool.ExpectExec("INSERT INTO evidence").WithArgs(dbEvidence, dbNode, dbSource, e.Quote, e.RefID, e.ClaimOrigin, e.InterviewID)
			if failure == "conflict" {
				insert.WillReturnResult(pgxmock.NewResult("INSERT", 0))
				pool.ExpectRollback()
				_, err := pg.UpdateNode(dbNode, patch)
				expectTreeError(t, err, "DUPLICATE_EVIDENCE_ID")
				return
			}
			insert.WillReturnResult(pgxmock.NewResult("INSERT", 1))
			deleteOld := pool.ExpectExec("DELETE FROM evidence").WithArgs(dbNode, []string{dbEvidence})
			if failure == "delete" {
				deleteOld.WillReturnError(errTestDB)
				pool.ExpectRollback()
			} else {
				deleteOld.WillReturnResult(pgxmock.NewResult("DELETE", 1))
				pool.ExpectQuery("SELECT COALESCE").WithArgs(dbNode).
					WillReturnRows(pgxmock.NewRows([]string{"evidence"}).AddRow(payload))
				commit := pool.ExpectCommit()
				if failure == "commit" {
					commit.WillReturnError(errTestDB)
					pool.ExpectRollback()
				}
			}
			got, err := pg.UpdateNode(dbNode, patch)
			if failure != "" {
				if !errors.Is(err, errTestDB) || got != nil {
					t.Fatalf("ошибка транзакции: %#v, %v", got, err)
				}
				return
			}
			if err != nil || got.ID != dbNode || got.Title != title || len(got.Evidence) != 1 || got.Evidence[0].Quote != e.Quote {
				t.Fatalf("обновлённый узел: %#v, %v", got, err)
			}
		})
	}
}

func TestPostgresRunsAndSourcesLists(t *testing.T) {
	pool, pg := testPostgresAPI(t)
	pool.ExpectQuery("FROM runs ORDER BY created_at DESC").WillReturnRows(pgxmock.NewRows([]string{"id", "profession", "status", "stage", "type", "created_by", "root", "started", "finished"}).
		AddRow(dbRun, "Инженер", domain.RunRunning, domain.StageAnalysis, domain.RunTypeBatch, "", nil, nil, nil))
	runs, err := pg.ListRuns()
	if err != nil || len(runs) != 1 || runs[0].ID != dbRun {
		t.Fatalf("список исследований: %#v, %v", runs, err)
	}
	pool.ExpectQuery("ORDER BY created_at ASC").
		WillReturnRows(pgxmock.NewRows([]string{"id", "profession", "status", "stage", "type", "started", "finished"}).
			AddRow(dbRun, "Инженер", domain.RunPending, domain.StageCreated, domain.RunTypeBatch, nil, nil))
	runs, err = pg.ListIncompleteRuns()
	if err != nil || len(runs) != 1 || runs[0].Status != domain.RunPending {
		t.Fatalf("незавершённые исследования: %#v, %v", runs, err)
	}
	when := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	sourceRows := func() *pgxmock.Rows {
		return pgxmock.NewRows([]string{"id", "type", "title", "content", "source_url", "created_at"}).
			AddRow(dbSource, domain.SourceType("document"), "Руководство", "Описание", "", &when)
	}
	pool.ExpectQuery("FROM sources WHERE run_id IS NULL").WithArgs(5).WillReturnRows(sourceRows())
	got, err := pg.ListGlobalSources(100)
	if err != nil || len(got) != 1 || got[0].ID != dbSource || !got[0].CreatedAt.Equal(when) {
		t.Fatalf("источники: %#v, %v", got, err)
	}
	pool.ExpectQuery("INSERT INTO sources").WithArgs(domain.SourceType("document"), "Руководство", "Описание", "").WillReturnRows(sourceRows())
	src, err := pg.CreateSource(domain.Source{Type: "document", Title: "Руководство", Content: "Описание"})
	if err != nil || src.ID != dbSource {
		t.Fatalf("созданный источник: %#v, %v", src, err)
	}
	pool.ExpectQuery("SELECT root_node_id IS NOT NULL").WithArgs(dbRun).
		WillReturnRows(pgxmock.NewRows([]string{"found"}).AddRow(true))
	if exists, err := pg.TreeExists(context.Background(), dbRun); err != nil || !exists {
		t.Fatalf("дерево исследования: %v, %v", exists, err)
	}
}

func TestPostgresSaveTreeConvertsHierarchyAndPreservesConcurrentWinner(t *testing.T) {
	pool, pg := testPostgresAPI(t)
	rootID := dbNode
	const childID = "805cd798-35d0-4637-ab57-00d9cd706629"
	nodes := []domain.Node{
		{ID: rootID, Type: domain.NodeRole, Title: "Инженер", Zun: &domain.Zun{Knowledge: []string{"Диагностика"}},
			Evidence: []domain.Evidence{{ID: dbEvidence, SourceID: dbSource, Quote: "Роль", RefID: "r1"}}},
		{ID: childID, ParentID: &rootID, Type: domain.NodeFunction, Title: "Проверка", Description: "Описание",
			Evidence: []domain.Evidence{{ID: dbEvidenceTwo, SourceID: dbSource, Quote: "Функция"}}},
	}
	run, root, source := pgUUID(t, dbRun), pgUUID(t, rootID), pgUUID(t, dbSource)
	zun, err := json.Marshal(nodes[0].Zun)
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	for _, concurrent := range []bool{false, true} {
		pool.ExpectQuery("FROM runs WHERE id=").WithArgs(dbRun).WillReturnRows(runRow())
		pool.ExpectBegin()
		for level, node := range nodes {
			id := pgUUID(t, node.ID)
			parent := pgtype.UUID{}
			if node.ParentID != nil {
				parent = pgUUID(t, *node.ParentID)
			}
			description := pgtype.Text{String: node.Description, Valid: node.Description != ""}
			pool.ExpectQuery("INSERT INTO nodes").WithArgs(id, run, parent, int16(level), string(node.Type), node.Title, description).
				WillReturnRows(pgxmock.NewRows([]string{"id", "run", "parent", "level", "type", "title", "description", "created", "updated", "zun", "review_status", "review_comment", "reviewed_at", "reviewed_by"}).
					AddRow(id, run, parent, int16(level), string(node.Type), node.Title, description, stamp, stamp, []byte(nil), "pending", "", nil, ""))
			citation := node.Evidence[0]
			citationID := pgUUID(t, citation.ID)
			ref := pgtype.Text{String: citation.RefID, Valid: citation.RefID != ""}
			pool.ExpectExec("INSERT INTO evidence").WithArgs(citationID, id, source, citation.Quote, false, ref, string(citation.ClaimOrigin), pgtype.UUID{}).
				WillReturnResult(pgxmock.NewResult("INSERT", 1))
		}
		update := pool.ExpectExec("UPDATE runs SET root_node_id").WithArgs(run, root)
		if concurrent {
			update.WillReturnResult(pgxmock.NewResult("UPDATE", 0))
			pool.ExpectRollback()
			pool.ExpectQuery("FROM runs WHERE id=").WithArgs(dbRun).
				WillReturnRows(pgxmock.NewRows([]string{"id", "profession", "status", "stage", "type", "created_by", "root", "started", "finished", "result_kind", "program_id"}).
					AddRow(dbRun, "Инженер", domain.RunDone, domain.StageDone, domain.RunTypeBatch, "", &rootID, nil, nil, "", ""))
		} else {
			update.WillReturnResult(pgxmock.NewResult("UPDATE", 1))
			pool.ExpectCommit()
			pool.ExpectExec("UPDATE nodes SET zun=").WithArgs(zun, rootID).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		}
		err := pg.SaveTree(context.Background(), dbRun, nodes)
		if (concurrent && !errors.Is(err, api.ErrTreeExists)) || (!concurrent && err != nil) {
			t.Fatalf("сохранение дерева при конкурирующей записи %v: %v", concurrent, err)
		}
	}
}
