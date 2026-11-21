package api

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/ingestion"
	"activity-descriptor/internal/programdocx"
)

var safeDOCXFilename = regexp.MustCompile(`^program-[a-z0-9._-]+\.docx$`)

func sampleProgramDraft() ProgramImportDraft {
	return ProgramImportDraft{
		ID:         testProgramDraftID,
		Status:     programDraftStatusDraft,
		Key:        "mirea/09.03.01/2024/full-time",
		SourceName: "fgos-09.03.01.pdf",
		CreatedAt:  time.Date(2024, 5, 17, 9, 5, 0, 0, time.UTC),
		Warnings:   []string{"страница 2 распознана OCR"},
		Program: ingestion.EducationProgram{
			Title:        "Программная инженерия",
			Code:         "09.03.01/Программа ИТ",
			Organization: "МИРЭА — Российский технологический университет",
			Competencies: []ingestion.ProgramCompetency{
				{
					Code:  "УК-1",
					Title: "Способен осуществлять поиск, критический анализ и синтез информации",
					Type:  "universal",
					KSA: []ingestion.ProgramKSA{
						{Kind: "knowledge", Title: "методы поиска информации"},
						{Kind: "skill", Title: "анализировать источники"},
						{Kind: "ability", Title: "навыками системного анализа"},
					},
				},
				{
					Code:  "ПК-2",
					Title: "Способен разрабатывать программное обеспечение",
					Type:  "professional",
				},
			},
		},
	}
}

func programDraftDOCXPath(id string) string {
	return "/api/v1/admin/programs/drafts/" + id + "/export.docx"
}

func readDOCXPart(t *testing.T, data []byte, name string) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ответ не является валидным ZIP/DOCX: %v", err)
	}
	for _, file := range reader.File {
		if file.Name != name {
			continue
		}
		opened, openErr := file.Open()
		if openErr != nil {
			t.Fatalf("открыть %s: %v", name, openErr)
		}
		content, readErr := io.ReadAll(opened)
		closeErr := opened.Close()
		if readErr != nil {
			t.Fatalf("прочитать %s: %v", name, readErr)
		}
		if closeErr != nil {
			t.Fatalf("закрыть %s: %v", name, closeErr)
		}
		return content
	}
	t.Fatalf("в DOCX нет части %s", name)
	return nil
}

func TestProgramDraftDOCXExport(t *testing.T) {
	_, fake, handler := newProgramImportTestServer(t)
	fake.seed(sampleProgramDraft())

	recorder := doProgramJSON(t, handler, http.MethodGet, programDraftDOCXPath(testProgramDraftID), "admin", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != programdocx.ContentType {
		t.Fatalf("Content-Type = %q, ожидали %q", got, programdocx.ContentType)
	}
	filename := programdocx.FileName("09.03.01/Программа ИТ")
	wantDisposition := fmt.Sprintf("attachment; filename=%q", filename)
	if got := recorder.Header().Get("Content-Disposition"); got != wantDisposition {
		t.Fatalf("Content-Disposition = %q, ожидали %q", got, wantDisposition)
	}
	if !safeDOCXFilename.MatchString(filename) {
		t.Fatalf("имя файла %q не безопасно", filename)
	}
	if len(filename) > 80 {
		t.Fatalf("имя файла %q длиннее 80 символов", filename)
	}

	documentXML := readDOCXPart(t, recorder.Body.Bytes(), "word/document.xml")
	body := string(documentXML)
	for _, want := range []string{
		"Образовательная программа",
		"Программная инженерия",
		"УК-1",
		"Способен осуществлять поиск, критический анализ и синтез информации",
		"Знать: методы поиска информации",
		"Уметь: анализировать источники",
		"Владеть: навыками системного анализа",
		"ПК-2",
		"Черновик. Требует проверки методистом.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("word/document.xml не содержит %q", want)
		}
	}
}

func TestProgramDraftDOCXExportNotFound(t *testing.T) {
	_, fake, handler := newProgramImportTestServer(t)
	fake.seed(sampleProgramDraft())

	recorder := doProgramJSON(t, handler, http.MethodGet,
		programDraftDOCXPath("33333333-3333-3333-3333-333333333333"), "admin", "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("ожидали 404, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
}

func TestProgramDraftDOCXExportForbidden(t *testing.T) {
	_, fake, handler := newProgramImportTestServer(t)
	fake.seed(sampleProgramDraft())

	recorder := doProgramJSON(t, handler, http.MethodGet, programDraftDOCXPath(testProgramDraftID), "user", "")
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("ожидали 403, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("403 должен быть JSON, Content-Type = %q", got)
	}
}

func TestProgramDraftDOCXExportUnauthorized(t *testing.T) {
	_, fake, handler := newProgramImportTestServer(t)
	fake.seed(sampleProgramDraft())

	recorder := doProgramJSON(t, handler, http.MethodGet, programDraftDOCXPath(testProgramDraftID), "", "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("ожидали 401, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
}

func TestProgramDraftDOCXExportRequiresPostgres(t *testing.T) {
	handler := NewServer(newMemStore()).Routes()
	recorder := doProgramJSON(t, handler, http.MethodGet, programDraftDOCXPath(testProgramDraftID), "admin", "")
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("in-memory store: ожидали 501, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
}

func TestProgramDraftDOCXExportSanitizesFilename(t *testing.T) {
	tests := []struct {
		name string
		code string
	}{
		{name: "кириллица и слэш", code: "09.03.01/Программа ИТ"},
		{name: "обход каталогов", code: `../..\etc/passwd`},
		{name: "инъекция заголовка", code: "ПК-1; filename=evil.docx\r\nX-Injected: 1"},
		{name: "длинный код", code: strings.Repeat("Очень длинный код ", 20)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, fake, handler := newProgramImportTestServer(t)
			draft := sampleProgramDraft()
			draft.Program.Code = test.code
			fake.seed(draft)

			recorder := doProgramJSON(t, handler, http.MethodGet, programDraftDOCXPath(testProgramDraftID), "admin", "")
			if recorder.Code != http.StatusOK {
				t.Fatalf("ожидали 200, получили %d (%s)", recorder.Code, recorder.Body.String())
			}
			disposition := recorder.Header().Get("Content-Disposition")
			const prefix = `attachment; filename="`
			if !strings.HasPrefix(disposition, prefix) || !strings.HasSuffix(disposition, `"`) {
				t.Fatalf("неожиданный Content-Disposition: %q", disposition)
			}
			filename := strings.TrimSuffix(strings.TrimPrefix(disposition, prefix), `"`)
			if !safeDOCXFilename.MatchString(filename) {
				t.Fatalf("имя файла %q содержит недопустимые символы", filename)
			}
			if len(filename) > 80 {
				t.Fatalf("имя файла %q длиннее 80 символов", filename)
			}
		})
	}
}
