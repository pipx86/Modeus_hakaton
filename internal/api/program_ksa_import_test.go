package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
)

// fakeKSAExtractor — LLM-адаптер без сети: страницы и KSA задаются тестом.
type fakeKSAExtractor struct {
	mu         sync.Mutex
	pages      []fgos.Page
	pagesErr   error
	entries    []fgos.KSAEntry
	warnings   []string
	extractErr error
	pagesCalls int
	ksaCalls   int
	deadline   time.Time
	hasLimit   bool
}

func (f *fakeKSAExtractor) ExtractPages(_ string, _ []byte) ([]fgos.Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pagesCalls++
	if f.pagesErr != nil {
		return nil, f.pagesErr
	}
	return f.pages, nil
}

func (f *fakeKSAExtractor) Extract(_ context.Context, _ []fgos.Page) (ingestion.EducationProgram, []string, error) {
	return ingestion.EducationProgram{}, nil, nil
}

func (f *fakeKSAExtractor) ExtractKSA(ctx context.Context, _ []fgos.Page) ([]fgos.KSAEntry, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ksaCalls++
	f.deadline, f.hasLimit = ctx.Deadline()
	if f.extractErr != nil {
		return nil, f.warnings, f.extractErr
	}
	return f.entries, f.warnings, nil
}

func (f *fakeKSAExtractor) calls() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pagesCalls, f.ksaCalls
}

func doProgramKSAImport(t *testing.T, handler http.Handler, id, role, filename, content string) *httptest.ResponseRecorder {
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
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/programs/drafts/"+id+"/ksa", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if role != "" {
		request.Header.Set("Authorization", "Bearer "+auth.TokenFor("test-"+role, auth.Role(role)))
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func seedKSADraft(fake *fakeProgramDraftStore) {
	fake.seed(ProgramImportDraft{
		ID:           testProgramDraftID,
		Key:          "mirea/09.03.01/2024/full-time",
		SourceName:   "fgos.pdf",
		SourceFormat: "pdf",
		Program: ingestion.EducationProgram{
			Title: "Программа",
			Competencies: []ingestion.ProgramCompetency{
				{
					Code:  "УК-1",
					Title: "Способен осуществлять поиск и критический анализ информации",
					Type:  "universal",
					KSA:   []ingestion.ProgramKSA{{Kind: "knowledge", Title: "методы системного анализа"}},
				},
				{
					Code:  "ОПК-2",
					Title: "Способен разрабатывать программное обеспечение",
					Type:  "general_professional",
				},
			},
		},
		Report: ProgramImportReport{Competencies: 2, KSA: 1, Warnings: []string{}},
	})
}

func ksaTestServer(t *testing.T, extractor fgos.DocumentExtractor) (*fakeProgramDraftStore, http.Handler) {
	t.Helper()
	fake := newFakeProgramDraftStore()
	seedKSADraft(fake)
	server := NewServer(fake)
	if extractor != nil {
		server.SetProgramTextExtractor(extractor)
	}
	return fake, server.Routes()
}

func ksaTestExtractor() *fakeKSAExtractor {
	return &fakeKSAExtractor{
		entries: []fgos.KSAEntry{
			{Code: "УК-1", KSA: []ingestion.ProgramKSA{
				{Kind: "knowledge", Title: "методы системного анализа"},
				{Kind: "skill", Title: "анализировать информацию"},
			}},
			{Code: "ОПК-2", KSA: []ingestion.ProgramKSA{
				{Kind: "ability", Title: "навыками отладки"},
			}},
			{Code: "ПК-9", KSA: []ingestion.ProgramKSA{
				{Kind: "knowledge", Title: "чужая компетенция"},
			}},
		},
		warnings: []string{"фрагмент 1: предупреждение KSA"},
	}
}

func TestProgramDraftKSAImportAddsAndIsIdempotent(t *testing.T) {
	extractor := ksaTestExtractor()
	fake, handler := ksaTestServer(t, extractor)

	recorder := doProgramKSAImport(t, handler, testProgramDraftID, "admin", "opop.md", "# ОПОП\nОПК-2: знать ...")
	if recorder.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	var result programKSAImportResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("ответ не JSON: %v (%s)", err, recorder.Body.String())
	}
	if result.DraftID != testProgramDraftID || result.Added != 2 || result.AlreadyPresent != 1 {
		t.Fatalf("ответ: %+v", result)
	}
	if len(result.UnknownCodes) != 1 || result.UnknownCodes[0] != "ПК-9" {
		t.Fatalf("unknown_codes: %+v", result.UnknownCodes)
	}
	if !hasWarning(result.Warnings, "фрагмент 1: предупреждение KSA") {
		t.Fatalf("warnings: %+v", result.Warnings)
	}
	if pagesCalls, ksaCalls := extractor.calls(); pagesCalls != 0 || ksaCalls != 1 {
		t.Fatalf("для MD страницы не извлекаются адаптером: pages=%d ksa=%d", pagesCalls, ksaCalls)
	}

	fake.mu.Lock()
	draft := fake.drafts[testProgramDraftID]
	fake.mu.Unlock()
	uk := draft.Program.Competencies[0]
	if uk.Title != "Способен осуществлять поиск и критический анализ информации" || uk.Type != "universal" {
		t.Fatalf("формулировка компетенции изменилась: %+v", uk)
	}
	if len(uk.KSA) != 2 || uk.KSA[1].Title != "анализировать информацию" {
		t.Fatalf("KSA УК-1: %+v", uk.KSA)
	}
	if len(draft.Program.Competencies[1].KSA) != 1 {
		t.Fatalf("KSA ОПК-2: %+v", draft.Program.Competencies[1].KSA)
	}
	if draft.Report.KSA != 3 || draft.Report.Competencies != 2 {
		t.Fatalf("отчёт не пересчитан: %+v", draft.Report)
	}

	repeat := doProgramKSAImport(t, handler, testProgramDraftID, "admin", "opop.md", "# ОПОП\nОПК-2: знать ...")
	if repeat.Code != http.StatusOK {
		t.Fatalf("повтор: ожидали 200, получили %d (%s)", repeat.Code, repeat.Body.String())
	}
	var repeated programKSAImportResult
	if err := json.Unmarshal(repeat.Body.Bytes(), &repeated); err != nil {
		t.Fatalf("повторный ответ не JSON: %v", err)
	}
	if repeated.Added != 0 || repeated.AlreadyPresent != 3 {
		t.Fatalf("повтор должен быть идемпотентным: %+v", repeated)
	}
	fake.mu.Lock()
	after := fake.drafts[testProgramDraftID]
	fake.mu.Unlock()
	if countProgramKSA(after.Program) != 3 {
		t.Fatalf("KSA размножились: %+v", after.Program)
	}
}

func TestProgramDraftKSAImportPDFUsesPageExtractor(t *testing.T) {
	extractor := &fakeKSAExtractor{
		pages:   []fgos.Page{{Number: 3, Text: "ОПК-2. Знать: алгоритмы."}},
		entries: []fgos.KSAEntry{{Code: "ОПК-2", KSA: []ingestion.ProgramKSA{{Kind: "knowledge", Title: "алгоритмы"}}}},
	}
	_, handler := ksaTestServer(t, extractor)

	recorder := doProgramKSAImport(t, handler, testProgramDraftID, "admin", "opop.pdf", "%PDF-1.4 fake")
	if recorder.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	if pagesCalls, ksaCalls := extractor.calls(); pagesCalls != 1 || ksaCalls != 1 {
		t.Fatalf("PDF должен идти через извлечение страниц: pages=%d ksa=%d", pagesCalls, ksaCalls)
	}
}

func TestProgramDraftKSAImportDOCXWarnsAboutPages(t *testing.T) {
	extractor := &fakeKSAExtractor{
		pages:   []fgos.Page{{Number: 1, Text: "ОПК-2. Знать: алгоритмы."}},
		entries: []fgos.KSAEntry{{Code: "ОПК-2", KSA: []ingestion.ProgramKSA{{Kind: "knowledge", Title: "алгоритмы"}}}},
	}
	_, handler := ksaTestServer(t, extractor)

	recorder := doProgramKSAImport(t, handler, testProgramDraftID, "admin", "opop.docx", "PK fake")
	if recorder.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	var result programKSAImportResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if !hasWarning(result.Warnings, fgos.ApproximatePageNumbersWarning) {
		t.Fatalf("нет предупреждения о страницах DOCX: %+v", result.Warnings)
	}
}

func TestProgramDraftKSAImportNotFound(t *testing.T) {
	_, handler := ksaTestServer(t, ksaTestExtractor())
	recorder := doProgramJSON(t, handler, http.MethodPost, "/api/v1/admin/programs/drafts/33333333-3333-3333-3333-333333333333/ksa", "admin", "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("ожидали 404, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
}

func TestProgramDraftKSAImportValidation(t *testing.T) {
	_, handler := ksaTestServer(t, ksaTestExtractor())

	if recorder := doProgramKSAImport(t, handler, testProgramDraftID, "admin", "", ""); recorder.Code != http.StatusBadRequest {
		t.Fatalf("без файла: ожидали 400, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	if recorder := doProgramKSAImport(t, handler, testProgramDraftID, "admin", "opop.md", ""); recorder.Code != http.StatusBadRequest {
		t.Fatalf("пустой файл: ожидали 400, получили %d", recorder.Code)
	}
	format := doProgramKSAImport(t, handler, testProgramDraftID, "admin", "opop.html", "<html>ОПК-2</html>")
	if format.Code != http.StatusUnprocessableEntity {
		t.Fatalf("HTML: ожидали 422, получили %d (%s)", format.Code, format.Body.String())
	}
	var body programImportErrorBody
	if err := json.Unmarshal(format.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if body.Code != "PROGRAM_KSA_INVALID" {
		t.Fatalf("code = %q", body.Code)
	}
	if recorder := doProgramKSAImport(t, handler, testProgramDraftID, "user", "opop.md", "текст"); recorder.Code != http.StatusForbidden {
		t.Fatalf("не admin: ожидали 403, получили %d", recorder.Code)
	}
}

func TestProgramDraftKSAImportNoKSA(t *testing.T) {
	extractor := &fakeKSAExtractor{extractErr: fmt.Errorf("%w: последняя ошибка — модель не вернула KSA", fgos.ErrNoKSA)}
	_, handler := ksaTestServer(t, extractor)
	recorder := doProgramKSAImport(t, handler, testProgramDraftID, "admin", "opop.md", "текст")
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ожидали 422, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	var body programImportErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if body.Code != "PROGRAM_KSA_NOT_FOUND" {
		t.Fatalf("code = %q", body.Code)
	}
	if len(body.Details) == 0 || !strings.Contains(body.Details[0], "модель не вернула KSA") {
		t.Fatalf("details: %+v", body.Details)
	}
}

func TestProgramDraftKSAImportLLMUnavailable(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode string
		wantHTTP int
	}{
		{"unavailable", fmt.Errorf("%w: upstream 500", fgos.ErrKSAUnavailable), "PROGRAM_KSA_LLM_UNAVAILABLE", http.StatusServiceUnavailable},
		{"no-client", fgos.ErrNoClient, "PROGRAM_KSA_LLM_UNAVAILABLE", http.StatusServiceUnavailable},
		{"timeout", fgos.ErrTimeout, "PROGRAM_KSA_LLM_TIMEOUT", http.StatusServiceUnavailable},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, handler := ksaTestServer(t, &fakeKSAExtractor{extractErr: testCase.err})
			recorder := doProgramKSAImport(t, handler, testProgramDraftID, "admin", "opop.md", "текст")
			if recorder.Code != testCase.wantHTTP {
				t.Fatalf("ожидали %d, получили %d (%s)", testCase.wantHTTP, recorder.Code, recorder.Body.String())
			}
			var body errorBody
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("ответ не JSON: %v (%s)", err, recorder.Body.String())
			}
			if body.Code != testCase.wantCode {
				t.Fatalf("code = %q, ожидали %q", body.Code, testCase.wantCode)
			}
		})
	}
}

func TestProgramDraftKSAImportNotConfigured(t *testing.T) {
	_, handler := ksaTestServer(t, nil)
	recorder := doProgramKSAImport(t, handler, testProgramDraftID, "admin", "opop.md", "текст")
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("без адаптера: ожидали 422, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	var body programImportErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if body.Code != "PROGRAM_KSA_LLM_NOT_CONFIGURED" {
		t.Fatalf("code = %q", body.Code)
	}
}

func TestProgramDraftKSAImportScanNeedsOCR(t *testing.T) {
	_, handler := ksaTestServer(t, &fakeKSAExtractor{pagesErr: fgos.ErrNoTextLayer})
	recorder := doProgramKSAImport(t, handler, testProgramDraftID, "admin", "scan.pdf", "%PDF-1.4 fake")
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ожидали 422, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	var body programImportErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if body.Code != "PROGRAM_KSA_INVALID" {
		t.Fatalf("code = %q", body.Code)
	}
	if joined := strings.Join(body.Details, " "); !strings.Contains(joined, "OCR_ENABLED=true") {
		t.Fatalf("нет подсказки про OCR: %+v", body.Details)
	}
}

func TestProgramDraftKSAImportInMemoryReturns501(t *testing.T) {
	handler := NewServer(newMemStore()).Routes()
	recorder := doProgramKSAImport(t, handler, testProgramDraftID, "admin", "opop.md", "текст")
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("in-memory: ожидали 501, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
}

func TestProgramDraftKSAImportPassesBudgetToContext(t *testing.T) {
	t.Setenv(envProgramImportTimeout, "120")
	extractor := ksaTestExtractor()
	_, handler := ksaTestServer(t, extractor)
	recorder := doProgramKSAImport(t, handler, testProgramDraftID, "admin", "opop.md", "текст")
	if recorder.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	extractor.mu.Lock()
	deadline, hasLimit := extractor.deadline, extractor.hasLimit
	extractor.mu.Unlock()
	if !hasLimit {
		t.Fatal("контекст без deadline")
	}
	if remaining := time.Until(deadline); remaining < 110*time.Second || remaining > 130*time.Second {
		t.Fatalf("бюджет должен быть 120с, получили %s", remaining)
	}
}

func TestIsKSAProgramFormat(t *testing.T) {
	supported := []string{"pdf", "docx", "txt", "md", "markdown"}
	for _, format := range supported {
		if !isKSAProgramFormat(format) {
			t.Errorf("%s должен поддерживаться", format)
		}
	}
	for _, format := range []string{"html", "csv", "json", ""} {
		if isKSAProgramFormat(format) {
			t.Errorf("%s не должен поддерживаться", format)
		}
	}
}

var _ fgos.DocumentExtractor = (*fakeKSAExtractor)(nil)
var _ fgos.KSAExtractor = (*fakeKSAExtractor)(nil)
