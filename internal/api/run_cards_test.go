package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v4"
)

// Карточки 10, 14 и 24: чистые хелперы отчёта о покрытии, границы доступа и
// разбор строк без реальной PostgreSQL (pgxmock).

const (
	runCardsRunID       = "11111111-1111-4111-8111-111111111111"
	runCardsInterviewID = "22222222-2222-4222-8222-222222222222"
	runCardsNodeID      = "33333333-3333-4333-8333-333333333333"
	runCardsNodeTwoID   = "44444444-4444-4444-8444-444444444444"
	runCardsSourceID    = "55555555-5555-4555-8555-555555555555"
	runCardsEvidenceID  = "66666666-6666-4666-8666-666666666666"
	runCardsProgramID   = "77777777-7777-4777-8777-777777777777"
	runCardsCompetency  = "88888888-8888-4888-8888-888888888888"
	runCardsCompetencyT = "99999999-9999-4999-8999-999999999999"
	runCardsLinkID      = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	runCardsLinkTwoID   = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	runCardsLinkThreeID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
)

// --- чистые хелперы отчёта о покрытии ---

func TestCoverageNodeStatus(t *testing.T) {
	link := func(status domain.ProgramLinkStatus) programCoverageLink {
		return programCoverageLink{LinkID: "link", Status: status}
	}
	tests := []struct {
		name  string
		links []programCoverageLink
		want  string
	}{
		{"нет связей", nil, coverageNone},
		{"approved", []programCoverageLink{link(domain.ProgramLinkApproved)}, coverageConfirmed},
		{"proposed", []programCoverageLink{link(domain.ProgramLinkProposed)}, coverageProposed},
		{"rejected", []programCoverageLink{link(domain.ProgramLinkRejected)}, coverageNone},
		{"approved важнее proposed", []programCoverageLink{
			link(domain.ProgramLinkProposed), link(domain.ProgramLinkApproved)}, coverageConfirmed},
		{"proposed важнее rejected", []programCoverageLink{
			link(domain.ProgramLinkRejected), link(domain.ProgramLinkProposed)}, coverageProposed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := coverageNodeStatus(test.links); got != test.want {
				t.Fatalf("status = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCoverageCompetencySummaryOnlyApproved(t *testing.T) {
	links := []programCoverageLink{
		{CompetencyID: runCardsCompetency, CompetencyTitle: "Анализ данных", Status: domain.ProgramLinkApproved},
		{CompetencyID: runCardsCompetency, CompetencyTitle: "Анализ данных", Status: domain.ProgramLinkApproved},
		{CompetencyID: runCardsCompetency, CompetencyTitle: "Анализ данных", Status: domain.ProgramLinkProposed},
		{CompetencyID: runCardsCompetencyT, CompetencyTitle: "Коммуникация", Status: domain.ProgramLinkApproved},
		{CompetencyID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", CompetencyTitle: "Проектирование", Status: domain.ProgramLinkRejected},
	}
	summary := coverageCompetencySummary(links)
	if len(summary) != 2 {
		t.Fatalf("в сводку должны попасть только approved-компетенции: %+v", summary)
	}
	// Порядок: сначала больше подтверждённых связей, затем по названию.
	if summary[0].CompetencyID != runCardsCompetency || summary[0].LinksCount != 2 {
		t.Fatalf("первая компетенция: %+v", summary[0])
	}
	if summary[1].CompetencyID != runCardsCompetencyT || summary[1].LinksCount != 1 {
		t.Fatalf("вторая компетенция: %+v", summary[1])
	}
}

func TestCoverageCompetencySummaryDeterministicOrder(t *testing.T) {
	// Одинаковое число связей сортируется по названию, затем по id, поэтому
	// отчёт не зависит от порядка строк, пришедших из БД.
	links := []programCoverageLink{
		{CompetencyID: "id-2", CompetencyTitle: "Бета", Status: domain.ProgramLinkApproved},
		{CompetencyID: "id-1", CompetencyTitle: "Альфа", Status: domain.ProgramLinkApproved},
		{CompetencyID: "id-3", CompetencyTitle: "Альфа", Status: domain.ProgramLinkApproved},
	}
	summary := coverageCompetencySummary(links)
	if len(summary) != 3 || summary[0].CompetencyID != "id-1" || summary[1].CompetencyID != "id-3" || summary[2].CompetencyID != "id-2" {
		t.Fatalf("порядок компетенций: %+v", summary)
	}
}

func TestCoverageCountsInvariant(t *testing.T) {
	nodes := []programCoverageNode{
		{NodeID: "1", Status: coverageConfirmed},
		{NodeID: "2", Status: coverageProposed},
		{NodeID: "3", Status: coverageNone},
		{NodeID: "4", Status: coverageProposed},
	}
	counts := coverageCounts(nodes)
	if counts.Confirmed != 1 || counts.Proposed != 2 || counts.None != 1 || counts.Total != 4 {
		t.Fatalf("counts = %+v", counts)
	}
	if counts.Confirmed+counts.Proposed+counts.None != counts.Total {
		t.Fatalf("инвариант суммы нарушен: %+v", counts)
	}
}

func TestBuildProgramCoverage(t *testing.T) {
	nodes := []coverageTreeNode{
		{ID: runCardsNodeID, Title: "Собирать требования", Type: domain.NodeTask},
		{ID: runCardsNodeTwoID, Title: "Отчитываться", Type: domain.NodeFunction},
	}
	links := []programCoverageLink{
		{LinkID: runCardsLinkID, NodeID: runCardsNodeID, CompetencyID: runCardsCompetency,
			CompetencyTitle: "Анализ данных", Status: domain.ProgramLinkApproved, Reason: "совпадение"},
		{LinkID: runCardsLinkTwoID, NodeID: runCardsNodeID, CompetencyID: runCardsCompetencyT,
			CompetencyTitle: "Коммуникация", Status: domain.ProgramLinkProposed, Reason: "похоже"},
		{LinkID: runCardsLinkThreeID, NodeID: runCardsNodeTwoID, CompetencyID: runCardsCompetencyT,
			CompetencyTitle: "Коммуникация", Status: domain.ProgramLinkRejected, Reason: "не подходит"},
	}
	report := buildProgramCoverage(runCardsRunID, runCardsProgramID, "Аналитика данных", nodes, links)
	if report.RunID != runCardsRunID || report.ProgramID == nil || *report.ProgramID != runCardsProgramID {
		t.Fatalf("программа в отчёте: %+v", report)
	}
	if report.ProgramTitle == nil || *report.ProgramTitle != "Аналитика данных" {
		t.Fatalf("название программы: %+v", report.ProgramTitle)
	}
	if len(report.Nodes) != 2 {
		t.Fatalf("узлы отчёта: %+v", report.Nodes)
	}
	if report.Nodes[0].Status != coverageConfirmed || len(report.Nodes[0].Links) != 2 {
		t.Fatalf("узел с approved-связью: %+v", report.Nodes[0])
	}
	// Узел только с отклонённой связью не покрыт, но связь остаётся видимой.
	if report.Nodes[1].Status != coverageNone || len(report.Nodes[1].Links) != 1 {
		t.Fatalf("узел с rejected-связью: %+v", report.Nodes[1])
	}
	wantCounts := programCoverageCounts{Confirmed: 1, Proposed: 0, None: 1, Total: 2}
	if report.Counts != wantCounts {
		t.Fatalf("counts = %+v, want %+v", report.Counts, wantCounts)
	}
	if len(report.Competencies) != 1 || report.Competencies[0].CompetencyID != runCardsCompetency ||
		report.Competencies[0].LinksCount != 1 {
		t.Fatalf("компетенции: %+v", report.Competencies)
	}
}

func TestBuildProgramCoverageEmpty(t *testing.T) {
	// Режим activity: программы нет, узлов нет — это валидный пустой отчёт, а
	// не ошибка. Срезы обязаны быть не-nil, чтобы JSON отдавал [].
	report := buildProgramCoverage(runCardsRunID, "", "", nil, nil)
	if report.ProgramID != nil || report.ProgramTitle != nil {
		t.Fatalf("программа должна быть null: %+v", report)
	}
	if report.Nodes == nil || report.Competencies == nil {
		t.Fatalf("срезы должны быть не-nil: %+v", report)
	}
	if len(report.Nodes) != 0 || len(report.Competencies) != 0 || report.Counts.Total != 0 {
		t.Fatalf("пустой отчёт: %+v", report)
	}
}

func TestBuildProgramCoverageWithoutLinks(t *testing.T) {
	nodes := []coverageTreeNode{
		{ID: "node-1", Title: "Роль", Type: domain.NodeRole},
		{ID: "node-2", Title: "Функция", Type: domain.NodeFunction},
	}
	report := buildProgramCoverage(runCardsRunID, runCardsProgramID, "Программа", nodes, nil)
	if report.Counts.None != 2 || report.Counts.Total != 2 || report.Counts.Confirmed != 0 {
		t.Fatalf("без связей все узлы none: %+v", report.Counts)
	}
	if len(report.Competencies) != 0 || report.Competencies == nil {
		t.Fatalf("competencies должен быть пустым массивом: %+v", report.Competencies)
	}
	for _, node := range report.Nodes {
		if node.Status != coverageNone || node.Links == nil || len(node.Links) != 0 {
			t.Fatalf("узел без связей: %+v", node)
		}
	}
}

func TestOptionalCoverageString(t *testing.T) {
	if optionalCoverageString("   ") != nil || optionalCoverageString("") != nil {
		t.Fatal("пустая строка должна давать null")
	}
	if got := optionalCoverageString("  Программа  "); got == nil || *got != "Программа" {
		t.Fatalf("строка должна обрезаться: %v", got)
	}
}

func TestProgramCoverageLinkJSONShape(t *testing.T) {
	// node_id остаётся только для группировки: контракт карточки 24 перечисляет
	// в links поля link_id, competency_id, competency_title, status, reason.
	link := programCoverageLink{
		LinkID:          "link-1",
		NodeID:          "node-internal",
		CompetencyID:    runCardsCompetency,
		CompetencyTitle: "Анализ данных",
		Status:          domain.ProgramLinkApproved,
		Reason:          "совпадение",
	}
	payload, err := json.Marshal(link)
	if err != nil {
		t.Fatalf("marshal link: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("unmarshal link: %v", err)
	}
	if _, exists := body["node_id"]; exists {
		t.Fatalf("node_id не должен попадать в JSON связи: %s", payload)
	}
	for _, key := range []string{"link_id", "competency_id", "competency_title", "status", "reason"} {
		if _, exists := body[key]; !exists {
			t.Fatalf("в связи нет поля %q: %s", key, payload)
		}
	}
}

// --- границы доступа HTTP-эндпоинтов (без PostgreSQL) ---

func runCardsPaths(runID, interviewID string) []string {
	return []string{
		"/api/v1/runs/" + runID + "/sources",
		"/api/v1/runs/" + runID + "/interviews/" + interviewID + "/evidence",
		"/api/v1/runs/" + runID + "/program-coverage",
	}
}

func TestRunCardsRequireAuth(t *testing.T) {
	handler := NewServer(newMemStore()).Routes()
	for _, path := range runCardsPaths(runCardsRunID, runCardsInterviewID) {
		if rec := do(t, handler, http.MethodGet, path, "", ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s без токена: status = %d, want 401", path, rec.Code)
		}
	}
}

func TestRunCardsForbidForeignRun(t *testing.T) {
	store := newMemStore()
	run, err := store.CreateRunForUser("Аналитик данных", "иной-владелец")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store).Routes()
	for _, path := range runCardsPaths(run.ID, runCardsInterviewID) {
		// do() выдаёт токен субъекта test-user, а прогон принадлежит другому.
		if rec := do(t, handler, http.MethodGet, path, "user", ""); rec.Code != http.StatusForbidden {
			t.Fatalf("%s чужого прогона: status = %d (%s), want 403", path, rec.Code, rec.Body.String())
		}
		if rec := do(t, handler, http.MethodGet, path, "specialist", ""); rec.Code != http.StatusForbidden {
			t.Fatalf("%s чужого прогона: status = %d, want 403", path, rec.Code)
		}
		if rec := do(t, handler, http.MethodGet, path, "admin", ""); rec.Code != http.StatusNotImplemented {
			t.Fatalf("%s админом: status = %d, want 501 (in-memory store)", path, rec.Code)
		}
	}
}

func TestRunCardsUnknownRunNotFound(t *testing.T) {
	handler := NewServer(newMemStore()).Routes()
	for _, path := range runCardsPaths("unknown-run", runCardsInterviewID) {
		if rec := do(t, handler, http.MethodGet, path, "user", ""); rec.Code != http.StatusNotFound {
			t.Fatalf("%s неизвестного прогона: status = %d (%s), want 404", path, rec.Code, rec.Body.String())
		}
	}
}

func TestRunCardsRequirePostgres(t *testing.T) {
	store := newMemStore()
	run, err := store.CreateRunForUser("Аналитик данных", "test-user")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store).Routes()
	for _, path := range runCardsPaths(run.ID, runCardsInterviewID) {
		for _, role := range []string{"user", "specialist", "admin"} {
			if rec := do(t, handler, http.MethodGet, path, role, ""); rec.Code != http.StatusNotImplemented {
				t.Fatalf("%s (%s): status = %d (%s), want 501", path, role, rec.Code, rec.Body.String())
			}
		}
	}
}

// --- разбор строк PostgreSQL без реальной БД ---

func runCardsMock(t *testing.T) pgxmock.PgxPoolIface {
	t.Helper()
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		pool.Close()
	})
	return pool
}

func runCardsTestUUID(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		t.Fatal(err)
	}
	return id
}

// runCardsGetRunRow — строка runs в том порядке, в котором её читает GetRun
// (этот же запрос выполняет s.allowInterviewAccess).
func runCardsGetRunRow(runID, createdBy string) *pgxmock.Rows {
	return pgxmock.NewRows([]string{"id", "profession", "status", "stage", "type", "created_by", "root", "started", "finished"}).
		AddRow(runID, "Аналитик данных", domain.RunDone, domain.StageDone, domain.RunTypeBatch, createdBy, nil, nil, nil)
}

func TestListRunSourceCardsPostgresMock(t *testing.T) {
	pool := runCardsMock(t)
	when := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	pool.ExpectQuery("FROM run_sources rs").WithArgs(runCardsRunID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "type", "title", "source_url", "created_at", "count"}).
			AddRow(runCardsSourceID, domain.SourceStandard, "Профстандарт", "https://example.org/std", when, int64(2)).
			AddRow(runCardsSourceID, domain.SourceInterview, "Интервью респондента", "", when, int64(0)))
	cards, err := NewPGStore(pool).ListRunSourceCards(runCardsRunID)
	if err != nil {
		t.Fatalf("ListRunSourceCards: %v", err)
	}
	if len(cards) != 2 {
		t.Fatalf("карточки источников: %+v", cards)
	}
	if cards[0].Type != domain.SourceStandard || cards[0].SourceURL != "https://example.org/std" ||
		cards[0].EvidenceCount != 2 || !cards[0].CreatedAt.Equal(when) {
		t.Fatalf("первая карточка: %+v", cards[0])
	}
	if cards[1].EvidenceCount != 0 || cards[1].Title != "Интервью респондента" {
		t.Fatalf("вторая карточка: %+v", cards[1])
	}
	if _, err = NewPGStore(pool).ListRunSourceCards("not-a-uuid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("некорректный id прогона: %v, want ErrNotFound", err)
	}
}

func TestListInterviewEvidencePostgresMock(t *testing.T) {
	pool := runCardsMock(t)
	pool.ExpectQuery("run_chat_messages").WithArgs(runCardsRunID, runCardsInterviewID).
		WillReturnRows(pgxmock.NewRows([]string{"evidence", "node", "node_title", "node_type", "quote", "source", "source_title", "status"}).
			AddRow(runCardsEvidenceID, runCardsNodeID, "Писать SQL-запросы", domain.NodeAction, "Каждую неделю пишу SQL.", runCardsSourceID, "Интервью №1", domain.EvidenceApproved).
			AddRow(runCardsEvidenceID, runCardsNodeTwoID, "Собирать требования", domain.NodeTask, "Собираю требования.", runCardsSourceID, "Интервью №1", ""))
	items, err := NewPGStore(pool).ListInterviewEvidence(runCardsRunID, runCardsInterviewID)
	if err != nil {
		t.Fatalf("ListInterviewEvidence: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("основания интервью: %+v", items)
	}
	if items[0].EvidenceID != runCardsEvidenceID || items[0].NodeType != domain.NodeAction ||
		items[0].SourceTitle != "Интервью №1" || items[0].Status != domain.EvidenceApproved {
		t.Fatalf("первое основание: %+v", items[0])
	}
	// Пустой статус нормализуется к pending: старые строки не содержали его.
	if items[1].Status != domain.EvidencePending {
		t.Fatalf("пустой статус должен стать pending: %+v", items[1])
	}
	if _, err = NewPGStore(pool).ListInterviewEvidence(runCardsRunID, "not-a-uuid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("некорректный id интервью: %v, want ErrNotFound", err)
	}
}

func TestRunProgramCoveragePostgresMock(t *testing.T) {
	pool := runCardsMock(t)
	pool.ExpectQuery("LEFT JOIN education_programs").
		WithArgs(runCardsRunID).
		WillReturnRows(pgxmock.NewRows([]string{"program_id", "program_title"}).
			AddRow(runCardsProgramID, "Аналитика данных"))
	pool.ExpectQuery("FROM nodes").WithArgs(runCardsRunID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "title", "type"}).
			AddRow(runCardsNodeID, "Собирать требования", domain.NodeTask).
			AddRow(runCardsNodeTwoID, "Отчитываться", domain.NodeFunction))
	pool.ExpectQuery("FROM activity_program_links l").WithArgs(runCardsRunID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "node", "competency", "competency_title", "status", "reason"}).
			AddRow(runCardsLinkID, runCardsNodeID, runCardsCompetency, "Анализ данных", domain.ProgramLinkApproved, "совпадение").
			AddRow(runCardsLinkTwoID, runCardsNodeID, runCardsCompetencyT, "Коммуникация", domain.ProgramLinkProposed, "похоже").
			AddRow(runCardsLinkThreeID, runCardsNodeTwoID, runCardsCompetencyT, "Коммуникация", domain.ProgramLinkRejected, "не подходит"))
	report, err := NewPGStore(pool).RunProgramCoverage(runCardsRunID)
	if err != nil {
		t.Fatalf("RunProgramCoverage: %v", err)
	}
	if report.ProgramID == nil || *report.ProgramID != runCardsProgramID || report.ProgramTitle == nil {
		t.Fatalf("программа: %+v", report)
	}
	wantCounts := programCoverageCounts{Confirmed: 1, Proposed: 0, None: 1, Total: 2}
	if report.Counts != wantCounts {
		t.Fatalf("counts = %+v, want %+v", report.Counts, wantCounts)
	}
	if len(report.Competencies) != 1 || report.Competencies[0].LinksCount != 1 {
		t.Fatalf("сводка компетенций: %+v", report.Competencies)
	}
}

func TestRunProgramCoverageMissingRun(t *testing.T) {
	pool := runCardsMock(t)
	pool.ExpectQuery("LEFT JOIN education_programs").WithArgs(runCardsRunID).WillReturnError(pgx.ErrNoRows)
	if _, err := NewPGStore(pool).RunProgramCoverage(runCardsRunID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("отсутствующее исследование: %v, want ErrNotFound", err)
	}
	if _, err := NewPGStore(pool).RunProgramCoverage("not-a-uuid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("некорректный id прогона: %v, want ErrNotFound", err)
	}
}

// --- HTTP-контракт на pgxmock: маршрут, доступ и форма JSON ---

func TestRunSourcesHTTPWithPostgresMock(t *testing.T) {
	pool := runCardsMock(t)
	when := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(runCardsRunID).WillReturnRows(runCardsGetRunRow(runCardsRunID, ""))
	pool.ExpectQuery("FROM run_sources rs").WithArgs(runCardsRunID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "type", "title", "source_url", "created_at", "count"}).
			AddRow(runCardsSourceID, domain.SourceStandard, "Профстандарт", "https://example.org/std", when, int64(3)))
	handler := NewServer(NewPGStore(pool)).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+runCardsRunID+"/sources", "user", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var body []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v (%s)", err, rec.Body.String())
	}
	if len(body) != 1 {
		t.Fatalf("карточки: %s", rec.Body.String())
	}
	for _, key := range []string{"id", "type", "title", "source_url", "created_at", "evidence_count"} {
		if _, exists := body[0][key]; !exists {
			t.Fatalf("в ответе нет поля %q: %s", key, rec.Body.String())
		}
	}
	if body[0]["evidence_count"] != float64(3) {
		t.Fatalf("evidence_count = %v", body[0]["evidence_count"])
	}
}

func TestInterviewEvidenceHTTPWithPostgresMock(t *testing.T) {
	pool := runCardsMock(t)
	when := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(runCardsRunID).WillReturnRows(runCardsGetRunRow(runCardsRunID, ""))
	pool.ExpectQuery("FROM run_interviews").
		WithArgs(runCardsTestUUID(t, runCardsInterviewID), runCardsTestUUID(t, runCardsRunID)).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "run_id", "respondent_name", "respondent_role", "respondent_ref",
			"status", "started_at", "finished_at", "created_at", "updated_at",
		}).AddRow(
			runCardsTestUUID(t, runCardsInterviewID), runCardsTestUUID(t, runCardsRunID),
			"Мария", "аналитик", "", "active", when, nil, when, when))
	pool.ExpectQuery("run_chat_messages").WithArgs(runCardsRunID, runCardsInterviewID).
		WillReturnRows(pgxmock.NewRows([]string{"evidence", "node", "node_title", "node_type", "quote", "source", "source_title", "status"}).
			AddRow(runCardsEvidenceID, runCardsNodeID, "Писать SQL-запросы", domain.NodeAction, "Пишу SQL.", runCardsSourceID, "Интервью №1", domain.EvidenceApproved))
	handler := NewServer(NewPGStore(pool)).Routes()
	rec := do(t, handler, http.MethodGet,
		"/api/v1/runs/"+runCardsRunID+"/interviews/"+runCardsInterviewID+"/evidence", "specialist", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var body []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v (%s)", err, rec.Body.String())
	}
	if len(body) != 1 {
		t.Fatalf("основания: %s", rec.Body.String())
	}
	for _, key := range []string{"evidence_id", "node_id", "node_title", "node_type", "quote", "source_id", "source_title", "status"} {
		if _, exists := body[0][key]; !exists {
			t.Fatalf("в ответе нет поля %q: %s", key, rec.Body.String())
		}
	}
}

func TestInterviewEvidenceForeignInterviewNotFound(t *testing.T) {
	pool := runCardsMock(t)
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(runCardsRunID).WillReturnRows(runCardsGetRunRow(runCardsRunID, ""))
	// Интервью принадлежит другому прогону или не существует: запрос по (id, run_id)
	// не находит строку, и хендлер обязан ответить 404, а не пустым списком.
	pool.ExpectQuery("FROM run_interviews").
		WithArgs(runCardsTestUUID(t, runCardsInterviewID), runCardsTestUUID(t, runCardsRunID)).
		WillReturnError(pgx.ErrNoRows)
	handler := NewServer(NewPGStore(pool)).Routes()
	rec := do(t, handler, http.MethodGet,
		"/api/v1/runs/"+runCardsRunID+"/interviews/"+runCardsInterviewID+"/evidence", "user", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d (%s), want 404", rec.Code, rec.Body.String())
	}
}

func TestProgramCoverageHTTPWithPostgresMock(t *testing.T) {
	pool := runCardsMock(t)
	pool.ExpectQuery("FROM runs WHERE id=").WithArgs(runCardsRunID).WillReturnRows(runCardsGetRunRow(runCardsRunID, ""))
	pool.ExpectQuery("LEFT JOIN education_programs").WithArgs(runCardsRunID).
		WillReturnRows(pgxmock.NewRows([]string{"program_id", "program_title"}).AddRow("", ""))
	pool.ExpectQuery("FROM nodes").WithArgs(runCardsRunID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "title", "type"}).
			AddRow(runCardsNodeID, "Собирать требования", domain.NodeTask))
	pool.ExpectQuery("FROM activity_program_links l").WithArgs(runCardsRunID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "node", "competency", "competency_title", "status", "reason"}))
	handler := NewServer(NewPGStore(pool)).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+runCardsRunID+"/program-coverage", "user", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var body struct {
		RunID        string                `json:"run_id"`
		ProgramID    *string               `json:"program_id"`
		ProgramTitle *string               `json:"program_title"`
		Nodes        []map[string]any      `json:"nodes"`
		Competencies []map[string]any      `json:"competencies"`
		Counts       programCoverageCounts `json:"counts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v (%s)", err, rec.Body.String())
	}
	if body.RunID != runCardsRunID || body.ProgramID != nil || body.ProgramTitle != nil {
		t.Fatalf("программа должна быть null: %s", rec.Body.String())
	}
	// Пустое состояние: узел none, competencies — пустой массив, а не null.
	if len(body.Nodes) != 1 || body.Nodes[0]["status"] != coverageNone || len(body.Competencies) != 0 {
		t.Fatalf("пустое покрытие: %s", rec.Body.String())
	}
	if body.Counts.Total != 1 || body.Counts.None != 1 || body.Counts.Confirmed != 0 {
		t.Fatalf("counts: %+v", body.Counts)
	}
	if !json.Valid(rec.Body.Bytes()) || !containsJSONArray(rec.Body.Bytes(), "competencies") {
		t.Fatalf("competencies должен быть массивом: %s", rec.Body.String())
	}
}

// containsJSONArray проверяет, что поле сериализовано как массив, а не null.
func containsJSONArray(payload []byte, field string) bool {
	return strings.Contains(string(payload), `"`+field+`":[`) ||
		strings.Contains(string(payload), `"`+field+`": [`)
}
