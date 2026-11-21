package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/fgos"
	"activity-descriptor/internal/ingestion"
	"activity-descriptor/internal/ocr"
)

const (
	testProgramDraftID = "11111111-1111-1111-1111-111111111111"
	testTaxonomyID     = "22222222-2222-2222-2222-222222222222"
)

type programPublishCall struct {
	key      string
	taxonomy string
	program  ingestion.EducationProgram
}

type fakeProgramDraftStore struct {
	Store
	mu         sync.Mutex
	drafts     map[string]ProgramImportDraft
	order      []string
	published  []programPublishCall
	report     ingestion.EducationReport
	publishErr error
	seq        int
}

func newFakeProgramDraftStore() *fakeProgramDraftStore {
	return &fakeProgramDraftStore{
		drafts: map[string]ProgramImportDraft{},
		report: ingestion.EducationReport{
			ProgramID:             "program-1",
			UnmatchedCompetencies: 1,
			UnknownTaxonomyCodes:  []string{"T9"},
		},
	}
}

func (f *fakeProgramDraftStore) CreateProgramImportDraft(_ context.Context, draft ProgramImportDraft) (ProgramImportDraft, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := testProgramDraftID
	if f.seq > 1 {
		id = "11111111-1111-1111-1111-" + strings.Repeat("0", 11) + string(rune('0'+f.seq))
	}
	draft.ID = id
	if draft.Status == "" {
		draft.Status = programDraftStatusDraft
	}
	draft.CreatedAt = time.Now().UTC()
	draft.Warnings = nonNilStrings(draft.Warnings)
	f.drafts[id] = draft
	f.order = append([]string{id}, f.order...)
	return draft, nil
}

func (f *fakeProgramDraftStore) ListProgramImportDrafts(_ context.Context) ([]ProgramImportDraftSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]ProgramImportDraftSummary, 0, len(f.order))
	for _, id := range f.order {
		draft, ok := f.drafts[id]
		if !ok {
			continue
		}
		items = append(items, ProgramImportDraftSummary{
			ID:         draft.ID,
			Key:        draft.Key,
			SourceName: draft.SourceName,
			Status:     draft.Status,
			CreatedAt:  draft.CreatedAt,
			Counts: ProgramImportCounts{
				Competencies: draft.Report.Competencies,
				KSA:          draft.Report.KSA,
				Warnings:     len(draft.Warnings),
			},
		})
	}
	return items, nil
}

func (f *fakeProgramDraftStore) GetProgramImportDraft(_ context.Context, id string) (ProgramImportDraft, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	draft, ok := f.drafts[id]
	if !ok {
		return ProgramImportDraft{}, ErrNotFound
	}
	return draft, nil
}

func (f *fakeProgramDraftStore) SaveProgramImportDraftProgram(_ context.Context, id string, program ingestion.EducationProgram, report ProgramImportReport) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	draft, ok := f.drafts[id]
	if !ok {
		return ErrNotFound
	}
	draft.Program = program
	draft.Report = report
	f.drafts[id] = draft
	return nil
}

func (f *fakeProgramDraftStore) MarkProgramImportDraftPublished(_ context.Context, id, decidedBy string, report ingestion.EducationReport) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	draft, ok := f.drafts[id]
	if !ok {
		return ErrNotFound
	}
	if draft.Status != programDraftStatusDraft {
		return errProgramDraftDecided
	}
	now := time.Now().UTC()
	draft.Status = programDraftStatusPublished
	draft.DecidedAt = &now
	draft.DecidedBy = decidedBy
	f.drafts[id] = draft
	f.report = report
	return nil
}

func (f *fakeProgramDraftStore) MarkProgramImportDraftRejected(_ context.Context, id, decidedBy, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	draft, ok := f.drafts[id]
	if !ok {
		return ErrNotFound
	}
	if draft.Status != programDraftStatusDraft {
		return errProgramDraftDecided
	}
	now := time.Now().UTC()
	draft.Status = programDraftStatusRejected
	draft.DecidedAt = &now
	draft.DecidedBy = decidedBy
	draft.Error = reason
	f.drafts[id] = draft
	return nil
}

func (f *fakeProgramDraftStore) PublishEducation(_ context.Context, key, taxonomy string, program ingestion.EducationProgram) (ingestion.EducationReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, programPublishCall{key: key, taxonomy: taxonomy, program: program})
	if f.publishErr != nil {
		return ingestion.EducationReport{}, f.publishErr
	}
	return f.report, nil
}

func (f *fakeProgramDraftStore) seed(draft ProgramImportDraft) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if draft.ID == "" {
		draft.ID = testProgramDraftID
	}
	if draft.Status == "" {
		draft.Status = programDraftStatusDraft
	}
	if draft.CreatedAt.IsZero() {
		draft.CreatedAt = time.Now().UTC()
	}
	draft.Warnings = nonNilStrings(draft.Warnings)
	f.drafts[draft.ID] = draft
	f.order = append([]string{draft.ID}, f.order...)
}

func newProgramImportTestServer(t *testing.T) (*Server, *fakeProgramDraftStore, http.Handler) {
	t.Helper()
	fake := newFakeProgramDraftStore()
	server := NewServer(fake)
	return server, fake, server.Routes()
}

func doProgramImport(t *testing.T, handler http.Handler, role, key, filename, content string) *httptest.ResponseRecorder {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if filename != "" {
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if key != "" {
		if err := writer.WriteField("key", key); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/programs/import", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if role != "" {
		request.Header.Set("Authorization", "Bearer "+auth.TokenFor("test-"+role, auth.Role(role)))
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func doProgramJSON(t *testing.T, handler http.Handler, method, path, role, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if role != "" {
		request.Header.Set("Authorization", "Bearer "+auth.TokenFor("test-"+role, auth.Role(role)))
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

const validProgramCSV = "kind;code;title;parent_code;taxonomy_codes;type\n" +
	"program;P1;Программа;;;\n" +
	"competency;K1;Компетенция;;;ПК\n" +
	"knowledge;K1;Знать стандарт;K1;;\n"

func TestProgramImportCreatesDraft(t *testing.T) {
	_, fake, handler := newProgramImportTestServer(t)
	recorder := doProgramImport(t, handler, "admin", "prog-key", "program.csv", validProgramCSV)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("ожидали 201, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	var result programImportResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("ответ не JSON: %v (%s)", err, recorder.Body.String())
	}
	if result.DraftID == "" || result.Status != programDraftStatusDraft {
		t.Fatalf("ответ: %+v", result)
	}
	if result.Report.Competencies != 1 || result.Report.KSA != 1 || len(result.Report.Warnings) != 1 {
		t.Fatalf("отчёт: %+v", result.Report)
	}
	if !strings.Contains(result.Report.Warnings[0], "старом формате") {
		t.Fatalf("ожидали предупреждение о старом ключе, получили %+v", result.Report.Warnings)
	}
	if len(result.Program.Competencies) != 1 || result.Program.Competencies[0].Type != "professional" {
		t.Fatalf("программа: %+v", result.Program)
	}
	draft := fake.drafts[result.DraftID]
	if draft.Key != "prog-key" || draft.SourceName != "program.csv" || draft.SourceFormat != "csv" {
		t.Fatalf("черновик: %+v", draft)
	}
}

func TestProgramImportCanonicalKeyHasNoWarning(t *testing.T) {
	_, fake, handler := newProgramImportTestServer(t)
	const key = "mirea/09.03.01/2024/full-time"
	recorder := doProgramImport(t, handler, "admin", key, "program.csv", validProgramCSV)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("ожидали 201, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	var result programImportResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if len(result.Report.Warnings) != 0 {
		t.Fatalf("канонический ключ не должен давать предупреждений: %+v", result.Report.Warnings)
	}
	if draft := fake.drafts[result.DraftID]; draft.Key != key {
		t.Fatalf("ключ черновика: %q", draft.Key)
	}
}

func TestProgramImportValidationDetails(t *testing.T) {
	_, _, handler := newProgramImportTestServer(t)
	invalid := "kind;code;title;parent_code;taxonomy_codes;type\n" +
		"program;P1;Программа;;;\n" +
		"competency;K1;Первая;;;ПК\n" +
		"competency;K1;Дубль;;;ПК\n"
	recorder := doProgramImport(t, handler, "admin", "prog-key", "program.csv", invalid)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ожидали 422, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	var body programImportErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if body.Code != "PROGRAM_IMPORT_INVALID" || len(body.Details) == 0 {
		t.Fatalf("ответ: %+v", body)
	}
	if !strings.Contains(strings.Join(body.Details, "\n"), "повтор кода") {
		t.Fatalf("details: %+v", body.Details)
	}
}

func TestProgramImportBadRequests(t *testing.T) {
	_, _, handler := newProgramImportTestServer(t)
	if recorder := doProgramImport(t, handler, "admin", "", "program.csv", validProgramCSV); recorder.Code != http.StatusBadRequest {
		t.Fatalf("без key: ожидали 400, получили %d", recorder.Code)
	}
	if recorder := doProgramImport(t, handler, "admin", "prog-key", "", validProgramCSV); recorder.Code != http.StatusBadRequest {
		t.Fatalf("без файла: ожидали 400, получили %d", recorder.Code)
	}
	if recorder := doProgramImport(t, handler, "user", "prog-key", "program.csv", validProgramCSV); recorder.Code != http.StatusForbidden {
		t.Fatalf("не admin: ожидали 403, получили %d", recorder.Code)
	}
	if recorder := doProgramImport(t, handler, "admin", "prog-key", "program.csv", ""); recorder.Code != http.StatusBadRequest {
		t.Fatalf("пустой файл: ожидали 400, получили %d", recorder.Code)
	}
}

func TestProgramDraftsRequirePostgresStore(t *testing.T) {
	handler := NewServer(newMemStore()).Routes()
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		path := "/api/v1/admin/programs/drafts"
		if method == http.MethodPost {
			path = "/api/v1/admin/programs/import"
		}
		var recorder *httptest.ResponseRecorder
		if method == http.MethodPost {
			recorder = doProgramImport(t, handler, "admin", "key", "program.csv", validProgramCSV)
		} else {
			recorder = doProgramJSON(t, handler, method, path, "admin", "")
		}
		if recorder.Code != http.StatusNotImplemented {
			t.Fatalf("%s: ожидали 501, получили %d", method, recorder.Code)
		}
	}
}

func TestProgramDraftsListAndGet(t *testing.T) {
	_, fake, handler := newProgramImportTestServer(t)
	fake.seed(ProgramImportDraft{
		ID:           testProgramDraftID,
		Key:          "prog-key",
		SourceName:   "program.csv",
		SourceFormat: "csv",
		Program: ingestion.EducationProgram{
			Code:  "P1",
			Title: "Программа",
			Competencies: []ingestion.ProgramCompetency{
				{Code: "K1", Title: "Компетенция", Type: "professional"},
			},
		},
		Report:   ProgramImportReport{Competencies: 1, KSA: 0, Warnings: []string{}},
		Warnings: []string{},
	})

	list := doProgramJSON(t, handler, http.MethodGet, "/api/v1/admin/programs/drafts", "admin", "")
	if list.Code != http.StatusOK {
		t.Fatalf("список: %d (%s)", list.Code, list.Body.String())
	}
	var summaries []ProgramImportDraftSummary
	if err := json.Unmarshal(list.Body.Bytes(), &summaries); err != nil {
		t.Fatalf("список не JSON: %v", err)
	}
	if len(summaries) != 1 || summaries[0].ID != testProgramDraftID || summaries[0].Counts.Competencies != 1 {
		t.Fatalf("список: %+v", summaries)
	}

	get := doProgramJSON(t, handler, http.MethodGet, "/api/v1/admin/programs/drafts/"+testProgramDraftID, "admin", "")
	if get.Code != http.StatusOK {
		t.Fatalf("черновик: %d (%s)", get.Code, get.Body.String())
	}
	var draft ProgramImportDraft
	if err := json.Unmarshal(get.Body.Bytes(), &draft); err != nil {
		t.Fatalf("черновик не JSON: %v", err)
	}
	if draft.Key != "prog-key" || len(draft.Program.Competencies) != 1 || draft.Program.Competencies[0].Type != "professional" {
		t.Fatalf("черновик: %+v", draft)
	}

	missing := doProgramJSON(t, handler, http.MethodGet, "/api/v1/admin/programs/drafts/33333333-3333-3333-3333-333333333333", "admin", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("отсутствующий черновик: ожидали 404, получили %d", missing.Code)
	}
}

func TestPublishProgramDraft(t *testing.T) {
	_, fake, handler := newProgramImportTestServer(t)
	fake.seed(ProgramImportDraft{
		ID:           testProgramDraftID,
		Key:          "prog-key",
		SourceName:   "program.csv",
		SourceFormat: "csv",
		Program: ingestion.EducationProgram{
			Code:  "P1",
			Title: "Программа",
			Competencies: []ingestion.ProgramCompetency{
				{Code: "K1", Title: "Компетенция", Type: "professional"},
			},
		},
		Report: ProgramImportReport{Competencies: 1, KSA: 0, Warnings: []string{}},
	})

	recorder := doProgramJSON(t, handler, http.MethodPost, "/api/v1/admin/programs/drafts/"+testProgramDraftID+"/publish", "admin", `{"taxonomy":"`+testTaxonomyID+`"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("публикация: %d (%s)", recorder.Code, recorder.Body.String())
	}
	var report ingestion.EducationReport
	if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
		t.Fatalf("отчёт не JSON: %v", err)
	}
	if report.ProgramID != "program-1" || report.UnmatchedCompetencies != 1 {
		t.Fatalf("отчёт: %+v", report)
	}
	fake.mu.Lock()
	calls := append([]programPublishCall(nil), fake.published...)
	draft := fake.drafts[testProgramDraftID]
	fake.mu.Unlock()
	if len(calls) != 1 || calls[0].key != "prog-key" || calls[0].taxonomy != testTaxonomyID {
		t.Fatalf("SaveEducation-вызов: %+v", calls)
	}
	if len(calls[0].program.Competencies) != 1 || calls[0].program.Competencies[0].Type != "professional" {
		t.Fatalf("программа в SaveEducation: %+v", calls[0].program)
	}
	if draft.Status != programDraftStatusPublished {
		t.Fatalf("статус черновика: %s", draft.Status)
	}

	repeat := doProgramJSON(t, handler, http.MethodPost, "/api/v1/admin/programs/drafts/"+testProgramDraftID+"/publish", "admin", "")
	if repeat.Code != http.StatusConflict {
		t.Fatalf("повторная публикация: ожидали 409, получили %d (%s)", repeat.Code, repeat.Body.String())
	}
	missing := doProgramJSON(t, handler, http.MethodPost, "/api/v1/admin/programs/drafts/33333333-3333-3333-3333-333333333333/publish", "admin", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("отсутствующий черновик: ожидали 404, получили %d", missing.Code)
	}
}

func TestPublishProgramDraftValidationAndPublisherFailure(t *testing.T) {
	_, fake, handler := newProgramImportTestServer(t)
	fake.seed(ProgramImportDraft{
		ID:      testProgramDraftID,
		Key:     "prog-key",
		Program: ingestion.EducationProgram{Code: "P1", Title: "Программа"},
	})
	badTaxonomy := doProgramJSON(t, handler, http.MethodPost, "/api/v1/admin/programs/drafts/"+testProgramDraftID+"/publish", "admin", `{"taxonomy":"not-a-uuid"}`)
	if badTaxonomy.Code != http.StatusBadRequest {
		t.Fatalf("некорректная taxonomy: ожидали 400, получили %d", badTaxonomy.Code)
	}

	fake.mu.Lock()
	fake.publishErr = errors.New("db down")
	fake.mu.Unlock()
	failed := doProgramJSON(t, handler, http.MethodPost, "/api/v1/admin/programs/drafts/"+testProgramDraftID+"/publish", "admin", "")
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("ошибка SaveEducation: ожидали 500, получили %d", failed.Code)
	}
	fake.mu.Lock()
	status := fake.drafts[testProgramDraftID].Status
	fake.mu.Unlock()
	if status != programDraftStatusDraft {
		t.Fatalf("при ошибке публикации черновик должен остаться draft: %s", status)
	}
}

func TestRejectProgramDraft(t *testing.T) {
	_, fake, handler := newProgramImportTestServer(t)
	fake.seed(ProgramImportDraft{ID: testProgramDraftID, Key: "prog-key", Status: programDraftStatusDraft})
	recorder := doProgramJSON(t, handler, http.MethodPost, "/api/v1/admin/programs/drafts/"+testProgramDraftID+"/reject", "admin", `{"reason":"нужно исправить"}`)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("отклонение: ожидали 204, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	draft := fake.drafts[testProgramDraftID]
	if draft.Status != programDraftStatusRejected || draft.Error != "нужно исправить" {
		t.Fatalf("черновик: %+v", draft)
	}
	repeat := doProgramJSON(t, handler, http.MethodPost, "/api/v1/admin/programs/drafts/"+testProgramDraftID+"/reject", "admin", "")
	if repeat.Code != http.StatusConflict {
		t.Fatalf("повторное отклонение: ожидали 409, получили %d", repeat.Code)
	}
	missing := doProgramJSON(t, handler, http.MethodPost, "/api/v1/admin/programs/drafts/33333333-3333-3333-3333-333333333333/reject", "admin", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("отсутствующий черновик: ожидали 404, получили %d", missing.Code)
	}
}

type draftOnlyStore struct {
	Store
	drafts *fakeProgramDraftStore
}

func (s *draftOnlyStore) CreateProgramImportDraft(ctx context.Context, draft ProgramImportDraft) (ProgramImportDraft, error) {
	return s.drafts.CreateProgramImportDraft(ctx, draft)
}

func (s *draftOnlyStore) ListProgramImportDrafts(ctx context.Context) ([]ProgramImportDraftSummary, error) {
	return s.drafts.ListProgramImportDrafts(ctx)
}

func (s *draftOnlyStore) GetProgramImportDraft(ctx context.Context, id string) (ProgramImportDraft, error) {
	return s.drafts.GetProgramImportDraft(ctx, id)
}

func (s *draftOnlyStore) SaveProgramImportDraftProgram(ctx context.Context, id string, program ingestion.EducationProgram, report ProgramImportReport) error {
	return s.drafts.SaveProgramImportDraftProgram(ctx, id, program, report)
}

func (s *draftOnlyStore) MarkProgramImportDraftPublished(ctx context.Context, id, decidedBy string, report ingestion.EducationReport) error {
	return s.drafts.MarkProgramImportDraftPublished(ctx, id, decidedBy, report)
}

func (s *draftOnlyStore) MarkProgramImportDraftRejected(ctx context.Context, id, decidedBy, reason string) error {
	return s.drafts.MarkProgramImportDraftRejected(ctx, id, decidedBy, reason)
}

func TestPublishWithoutPublisherReturns501(t *testing.T) {
	store := &draftOnlyStore{drafts: newFakeProgramDraftStore()}
	server := NewServer(store)
	recorder := doProgramJSON(t, server.Routes(), http.MethodPost, "/api/v1/admin/programs/drafts/"+testProgramDraftID+"/publish", "admin", "")
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("без publisher: ожидали 501, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
}

// fakeProgramExtractor — LLM-адаптер ФГОС без сети для тестов импорта.
type fakeProgramExtractor struct {
	mu         sync.Mutex
	pages      []fgos.Page
	program    ingestion.EducationProgram
	warnings   []string
	pagesErr   error
	extractErr error
	pagesCalls int
	extract    int
}

func (f *fakeProgramExtractor) ExtractPages(_ string, _ []byte) ([]fgos.Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pagesCalls++
	if f.pagesErr != nil {
		return nil, f.pagesErr
	}
	return f.pages, nil
}

func (f *fakeProgramExtractor) Extract(_ context.Context, _ []fgos.Page) (ingestion.EducationProgram, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.extract++
	if f.extractErr != nil {
		return ingestion.EducationProgram{}, nil, f.extractErr
	}
	return f.program, f.warnings, nil
}

func (f *fakeProgramExtractor) calls() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pagesCalls, f.extract
}

func fgosTestProgram() ingestion.EducationProgram {
	return ingestion.EducationProgram{
		Competencies: []ingestion.ProgramCompetency{{
			Code:  "УК-1",
			Title: "Способен осуществлять поиск и критический анализ информации",
			Type:  "universal",
			KSA:   []ingestion.ProgramKSA{{Kind: "knowledge", Title: "методы системного анализа"}},
		}},
	}
}

func testProgramImportLLMServer(t *testing.T, extractor fgos.DocumentExtractor) (*fakeProgramDraftStore, http.Handler) {
	t.Helper()
	fake := newFakeProgramDraftStore()
	server := NewServer(fake)
	server.SetProgramTextExtractor(extractor)
	return fake, server.Routes()
}

func (f *fakeProgramDraftStore) onlyDraft(t *testing.T) ProgramImportDraft {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.order) != 1 {
		t.Fatalf("ожидали один черновик, получили %d", len(f.order))
	}
	return f.drafts[f.order[0]]
}

func hasWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if warning == want {
			return true
		}
	}
	return false
}

func TestProgramImportPDFWithFakeExtractor(t *testing.T) {
	extractor := &fakeProgramExtractor{
		pages:    []fgos.Page{{Number: 1, Text: "УК-1. Способен осуществлять поиск."}},
		program:  fgosTestProgram(),
		warnings: []string{"фрагмент 1: предупреждение адаптера"},
	}
	fake, handler := testProgramImportLLMServer(t, extractor)
	recorder := doProgramImport(t, handler, "admin", "mirea/09.03.01/2024/full-time", "fgos.pdf", "%PDF-1.4 fake")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("ожидали 201, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	var result programImportResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if result.DraftID == "" || result.Status != programDraftStatusDraft {
		t.Fatalf("ответ: %+v", result)
	}
	if result.Report.Competencies != 1 || result.Report.KSA != 1 {
		t.Fatalf("отчёт: %+v", result.Report)
	}
	draft := fake.onlyDraft(t)
	if draft.SourceFormat != "pdf" || draft.SourceName != "fgos.pdf" {
		t.Fatalf("черновик: %+v", draft)
	}
	if draft.Program.Title != "fgos" {
		t.Fatalf("title из имени файла: %q", draft.Program.Title)
	}
	if !hasWarning(draft.Warnings, "фрагмент 1: предупреждение адаптера") {
		t.Fatalf("предупреждения адаптера потерялись: %v", draft.Warnings)
	}
	if pagesCalls, extractCalls := extractor.calls(); pagesCalls != 1 || extractCalls != 1 {
		t.Fatalf("вызовы адаптера: pages=%d extract=%d", pagesCalls, extractCalls)
	}
}

func TestProgramImportPDFWithoutLLMReturns422(t *testing.T) {
	_, _, handler := newProgramImportTestServer(t)
	recorder := doProgramImport(t, handler, "admin", "mirea/09.03.01/2024/full-time", "fgos.pdf", "%PDF-1.4 fake")
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("без LLM: ожидали 422, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	var body programImportErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if body.Code != "PROGRAM_IMPORT_LLM_NOT_CONFIGURED" {
		t.Fatalf("code = %q", body.Code)
	}
	if !strings.Contains(body.Message, "LLM") {
		t.Fatalf("понятное сообщение: %q", body.Message)
	}
}

func TestProgramImportDOCXWarnsAboutApproximatePages(t *testing.T) {
	extractor := &fakeProgramExtractor{
		pages:   []fgos.Page{{Number: 1, Text: "ОПК-2. Способен разрабатывать ПО."}},
		program: fgosTestProgram(),
	}
	fake, handler := testProgramImportLLMServer(t, extractor)
	recorder := doProgramImport(t, handler, "admin", "mirea/09.03.01/2024/full-time", "fgos.docx", "PK fake docx")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("ожидали 201, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	draft := fake.onlyDraft(t)
	if draft.SourceFormat != "docx" {
		t.Fatalf("source_format: %q", draft.SourceFormat)
	}
	if len(draft.Warnings) == 0 || draft.Warnings[0] != fgos.ApproximatePageNumbersWarning {
		t.Fatalf("предупреждение о приблизительных страницах: %v", draft.Warnings)
	}
}

func TestProgramImportPDFExtractionErrors(t *testing.T) {
	noText := &fakeProgramExtractor{pagesErr: fgos.ErrNoTextLayer}
	_, handler := testProgramImportLLMServer(t, noText)
	recorder := doProgramImport(t, handler, "admin", "mirea/09.03.01/2024/full-time", "scan.pdf", "%PDF-1.4 fake")
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("скан: ожидали 422, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	var body programImportErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if body.Code != "PROGRAM_IMPORT_INVALID" || len(body.Details) == 0 || !strings.Contains(body.Details[0], "нет текстового слоя") {
		t.Fatalf("ответ: %+v", body)
	}

	timedOut := &fakeProgramExtractor{extractErr: fgos.ErrTimeout}
	_, handler = testProgramImportLLMServer(t, timedOut)
	recorder = doProgramImport(t, handler, "admin", "mirea/09.03.01/2024/full-time", "fgos.pdf", "%PDF-1.4 fake")
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("таймаут: ожидали 503, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	var timeoutBody errorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &timeoutBody); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if timeoutBody.Code != "PROGRAM_IMPORT_LLM_TIMEOUT" {
		t.Fatalf("code = %q", timeoutBody.Code)
	}

	unavailable := &fakeProgramExtractor{extractErr: errors.New("upstream 500")}
	_, handler = testProgramImportLLMServer(t, unavailable)
	recorder = doProgramImport(t, handler, "admin", "mirea/09.03.01/2024/full-time", "fgos.pdf", "%PDF-1.4 fake")
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("недоступная модель: ожидали 503, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
}

func TestProgramImportScanWithoutOCRAddsHint(t *testing.T) {
	extractor := &fakeProgramExtractor{pagesErr: fgos.ErrNoTextLayer}
	_, handler := testProgramImportLLMServer(t, extractor)
	recorder := doProgramImport(t, handler, "admin", "mirea/09.03.01/2024/full-time", "scan.pdf", "%PDF-1.4 fake")
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ожидали 422, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	var body programImportErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	joined := strings.Join(body.Details, " ")
	if !strings.Contains(joined, "OCR_ENABLED=true") || !strings.Contains(joined, ocr.DocsPath) {
		t.Fatalf("нет подсказки про OCR: %+v", body.Details)
	}
}

func TestProgramImportImageWithoutOCRReturns422(t *testing.T) {
	extractor := &fakeProgramExtractor{}
	_, handler := testProgramImportLLMServer(t, extractor)
	recorder := doProgramImport(t, handler, "admin", "mirea/09.03.01/2024/full-time", "scan.png", "fake png")
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("без OCR: ожидали 422, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	var body programImportErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if body.Code != "PROGRAM_IMPORT_OCR_DISABLED" {
		t.Fatalf("code = %q", body.Code)
	}
	if joined := strings.Join(body.Details, " "); !strings.Contains(joined, ocr.DocsPath) {
		t.Fatalf("нет инструкции: %+v", body.Details)
	}
}

func TestProgramImportImageWithOCRReturnsCreated(t *testing.T) {
	extractor := &fakeProgramExtractor{
		pages:    []fgos.Page{{Number: 1, Text: "УК-1. Способен осуществлять поиск.", OCR: true}},
		program:  fgosTestProgram(),
		warnings: []string{fgos.OCRPageWarning(1)},
	}
	fake := newFakeProgramDraftStore()
	server := NewServer(fake)
	server.SetProgramTextExtractor(extractor)
	server.SetProgramOCREnabled(true)
	recorder := doProgramImport(t, server.Routes(), "admin", "mirea/09.03.01/2024/full-time", "scan.png", "fake png")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("с OCR: ожидали 201, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	draft := fake.onlyDraft(t)
	if draft.SourceFormat != "png" {
		t.Fatalf("source_format = %q", draft.SourceFormat)
	}
	if !hasWarning(draft.Warnings, fgos.OCRPageWarning(1)) {
		t.Fatalf("предупреждение OCR потерялось: %v", draft.Warnings)
	}
}

func TestProgramImportOCRErrors(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
		wantBody string
	}{
		{"disabled", ocr.ErrDisabled, http.StatusUnprocessableEntity, "PROGRAM_IMPORT_OCR_DISABLED"},
		{"unavailable", ocr.ErrUnavailable, http.StatusServiceUnavailable, "PROGRAM_IMPORT_OCR_UNAVAILABLE"},
		{"page_limit", ocr.ErrPageLimit, http.StatusUnprocessableEntity, "PROGRAM_IMPORT_OCR_PAGE_LIMIT"},
		{"timeout", ocr.ErrTimeout, http.StatusServiceUnavailable, "PROGRAM_IMPORT_OCR_TIMEOUT"},
		{"no_text", ocr.ErrNoText, http.StatusUnprocessableEntity, "PROGRAM_IMPORT_INVALID"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			extractor := &fakeProgramExtractor{pagesErr: testCase.err}
			_, handler := testProgramImportLLMServer(t, extractor)
			recorder := doProgramImport(t, handler, "admin", "mirea/09.03.01/2024/full-time", "scan.pdf", "%PDF-1.4 fake")
			if recorder.Code != testCase.wantCode {
				t.Fatalf("ожидали %d, получили %d (%s)", testCase.wantCode, recorder.Code, recorder.Body.String())
			}
			var body programImportErrorBody
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("ответ не JSON: %v", err)
			}
			if body.Code != testCase.wantBody {
				t.Fatalf("code = %q, ожидали %q", body.Code, testCase.wantBody)
			}
		})
	}
}

// fakeContextExtractor запоминает deadline контекста, который API передал в
// OCR-адаптер.
type fakeContextExtractor struct {
	*fakeProgramExtractor
	deadline time.Time
	hasLimit bool
}

func (f *fakeContextExtractor) ExtractPagesContext(ctx context.Context, name string, data []byte) ([]fgos.Page, error) {
	f.deadline, f.hasLimit = ctx.Deadline()
	return f.fakeProgramExtractor.ExtractPages(name, data)
}

func TestProgramImportPassesConfiguredBudgetToContext(t *testing.T) {
	t.Setenv(envProgramImportTimeout, "120")
	extractor := &fakeContextExtractor{fakeProgramExtractor: &fakeProgramExtractor{
		pages:   []fgos.Page{{Number: 1, Text: "УК-1. Способен осуществлять поиск.", OCR: true}},
		program: fgosTestProgram(),
	}}
	fake := newFakeProgramDraftStore()
	server := NewServer(fake)
	server.SetProgramTextExtractor(extractor)
	server.SetProgramOCREnabled(true)
	recorder := doProgramImport(t, server.Routes(), "admin", "mirea/09.03.01/2024/full-time", "scan.pdf", "%PDF-1.4 fake")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("ожидали 201, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	if !extractor.hasLimit {
		t.Fatal("контекст без deadline")
	}
	if remaining := time.Until(extractor.deadline); remaining < 110*time.Second || remaining > 130*time.Second {
		t.Fatalf("бюджет импорта должен быть 120с, получили %s", remaining)
	}
}

func TestProgramImportTimeoutFromEnv(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"unset", "", defaultProgramImportTimeout},
		{"seconds", "120", 2 * time.Minute},
		{"duration", "15m", 15 * time.Minute},
		{"invalid", "soon", defaultProgramImportTimeout},
		{"negative", "-5", defaultProgramImportTimeout},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv(envProgramImportTimeout, testCase.value)
			if got := programImportTimeoutFromEnv(); got != testCase.want {
				t.Fatalf("PROGRAM_IMPORT_TIMEOUT_S=%q: получили %s, ожидали %s", testCase.value, got, testCase.want)
			}
		})
	}
}
