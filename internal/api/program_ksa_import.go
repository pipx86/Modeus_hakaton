package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/fgos"
	"activity-descriptor/internal/ingestion"
	"activity-descriptor/internal/ocr"
)

// programKSARequestLimit ограничивает весь multipart-запрос: файл программы
// плюс накладные расходы формы.
const programKSARequestLimit = ingestion.MaxDocumentSize + (1 << 20)

// programKSAImportResult — ответ на добавление KSA в черновик. Счётчики
// описывают только текущий вызов, поэтому повторная загрузка того же документа
// даёт added=0 и already_present по числу уже сохранённых пунктов.
type programKSAImportResult struct {
	DraftID        string   `json:"draft_id"`
	Added          int      `json:"added"`
	AlreadyPresent int      `json:"already_present"`
	UnknownCodes   []string `json:"unknown_codes"`
	Warnings       []string `json:"warnings"`
}

// handleProgramDraftKSAImport godoc
// @Summary      Добавить KSA из ОПОП/РПД в черновик образовательной программы
// @Description  Принимает PDF/DOCX/TXT/MD с текстом ОПОП или РПД, извлекает знания, умения и навыки через LLM-адаптер и дописывает их в существующий черновик по коду компетенции. Формулировки компетенций не меняются; неизвестные коды возвращаются в unknown_codes и не создают компетенции. Повторная загрузка того же документа идемпотентна: added=0, already_present — число уже сохранённых KSA. Только admin.
// @Tags         academic
// @Accept       multipart/form-data
// @Produce      json
// @Param        id    path      string  true  "Draft ID"
// @Param        file  formData  file    true  "Документ ОПОП/РПД (PDF, DOCX, TXT или MD)"
// @Success      200   {object}  programKSAImportResult
// @Failure      400   {object}  errorBody
// @Failure      401   {object}  errorBody
// @Failure      403   {object}  errorBody
// @Failure      404   {object}  errorBody
// @Failure      413   {object}  errorBody
// @Failure      422   {object}  programImportErrorBody
// @Failure      500   {object}  errorBody
// @Failure      501   {object}  errorBody
// @Failure      503   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/admin/programs/drafts/{id}/ksa [post]
func (s *Server) handleProgramDraftKSAImport(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	drafts, ok := s.programDrafts()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "добавление KSA в черновик программы требует PostgreSQL")
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "не указан идентификатор черновика")
		return
	}
	draft, err := drafts.GetProgramImportDraft(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "черновик программы не найден")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось загрузить черновик программы")
		return
	}

	extractor, ok := s.ksaExtractor()
	if !ok {
		// 422, как у импорта PDF/DOCX: запрос осмысленный, но сервер не умеет
		// извлекать KSA без LLM-адаптера.
		writeJSON(w, http.StatusUnprocessableEntity, programImportErrorBody{
			Code:    "PROGRAM_KSA_LLM_NOT_CONFIGURED",
			Message: "извлечение KSA из ОПОП/РПД требует LLM-адаптера ФГОС: задайте LLM_API_KEY на сервере",
			Details: []string{"без LLM нельзя извлечь знания, умения и навыки из документа"},
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, programKSARequestLimit)
	if err := r.ParseMultipartForm(programKSARequestLimit); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "не удалось прочитать форму или файл больше 20 МБ")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "нужен файл в поле file")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, ingestion.MaxDocumentSize+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "не удалось прочитать файл")
		return
	}
	if len(data) == 0 {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "файл пуст")
		return
	}
	if len(data) > ingestion.MaxDocumentSize {
		writeErr(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "файл больше 20 МБ")
		return
	}

	sourceName := filepath.Base(strings.TrimSpace(header.Filename))
	if sourceName == "" || sourceName == "." {
		sourceName = "opop"
	}
	format := documentFormat(sourceName)
	if !isKSAProgramFormat(format) {
		writeJSON(w, http.StatusUnprocessableEntity, programImportErrorBody{
			Code:    "PROGRAM_KSA_INVALID",
			Message: "формат файла не поддерживается",
			Details: []string{fmt.Sprintf("формат %q не поддерживается: загрузите PDF, DOCX, TXT или MD", format)},
		})
		return
	}

	timeout := s.programImportTimeout()
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	warnings := []string{}
	pages, pageWarnings, pageErr := s.extractKSAPages(ctx, sourceName, format, data)
	warnings = append(warnings, pageWarnings...)
	if pageErr != nil {
		s.writeProgramKSAError(w, pageErr, timeout)
		return
	}

	entries, ksaWarnings, err := extractor.ExtractKSA(ctx, pages)
	warnings = append(warnings, ksaWarnings...)
	if err != nil {
		s.writeProgramKSAError(w, err, timeout)
		return
	}

	merged := fgos.MergeKSA(&draft.Program, entries)
	report := draft.Report
	report.Competencies = len(draft.Program.Competencies)
	report.KSA = countProgramKSA(draft.Program)
	report.Warnings = nonNilStrings(report.Warnings)
	if err = drafts.SaveProgramImportDraftProgram(r.Context(), id, draft.Program, report); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "черновик программы не найден")
			return
		}
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось сохранить KSA в черновике программы")
		return
	}

	writeJSON(w, http.StatusOK, programKSAImportResult{
		DraftID:        id,
		Added:          merged.Added,
		AlreadyPresent: merged.AlreadyPresent,
		UnknownCodes:   nonNilStrings(merged.UnknownCodes),
		Warnings:       nonNilStrings(warnings),
	})
}

// ksaExtractor возвращает адаптер, умеющий извлекать KSA. В продакшене это
// *fgos.Extractor или ocr.FGOSExtractor с встроенным *fgos.Extractor; старые
// реализации DocumentExtractor интерфейс не удовлетворяют.
func (s *Server) ksaExtractor() (fgos.KSAExtractor, bool) {
	if s == nil || s.programTextExtractor == nil {
		return nil, false
	}
	extractor, ok := s.programTextExtractor.(fgos.KSAExtractor)
	return extractor, ok
}

// extractKSAPages получает страницы документа: PDF/DOCX — существующим
// механизмом адаптера (с OCR, если он включён), TXT/MD — как одну страницу.
// HTML намеренно не поддерживается: у ОПОП/РПД должен быть файл.
func (s *Server) extractKSAPages(ctx context.Context, sourceName, format string, data []byte) ([]fgos.Page, []string, error) {
	switch format {
	case "txt", "md", "markdown":
		text, err := ingestion.ExtractDocumentText(sourceName, data)
		if err != nil {
			return nil, nil, fmt.Errorf("текстовый документ: %w", err)
		}
		return []fgos.Page{{Number: 1, Text: text}}, []string{}, nil
	default:
		pages, err := s.extractProgramPages(ctx, sourceName, data)
		if err != nil {
			return nil, nil, err
		}
		warnings := []string{}
		if warning := fgos.ApproximatePageNumbersWarningFor(format); warning != "" {
			warnings = append(warnings, warning)
		}
		return pages, warnings, nil
	}
}

// isKSAProgramFormat сообщает, из каких файлов эндпоинт KSA умеет читать текст.
func isKSAProgramFormat(format string) bool {
	switch format {
	case "pdf", "docx", "txt", "md", "markdown":
		return true
	default:
		return false
	}
}

// writeProgramKSAError переводит ошибку извлечения страниц или KSA в
// HTTP-ответ: 422 — документ или содержимое не подходят, 503 — модель/OCR
// недоступны или не успели.
func (s *Server) writeProgramKSAError(w http.ResponseWriter, err error, timeout time.Duration) {
	details := programImportProblems(err)
	switch {
	case errors.Is(err, ocr.ErrDisabled):
		writeJSON(w, http.StatusUnprocessableEntity, programImportErrorBody{
			Code:    "PROGRAM_KSA_OCR_DISABLED",
			Message: "документ является сканом: OCR на сервере выключен",
			Details: details,
		})
	case errors.Is(err, ocr.ErrUnavailable), errors.Is(err, ocr.ErrToolNotFound):
		writeJSON(w, http.StatusServiceUnavailable, programImportErrorBody{
			Code:    "PROGRAM_KSA_OCR_UNAVAILABLE",
			Message: "OCR недоступен: установите tesseract и poppler на сервере или повторите позже",
			Details: details,
		})
	case errors.Is(err, ocr.ErrPageLimit):
		writeJSON(w, http.StatusUnprocessableEntity, programImportErrorBody{
			Code:    "PROGRAM_KSA_OCR_PAGE_LIMIT",
			Message: "скан слишком большой для OCR: разделите документ или увеличьте OCR_MAX_PAGES",
			Details: details,
		})
	case errors.Is(err, ocr.ErrTimeout):
		writeJSON(w, http.StatusServiceUnavailable, programImportErrorBody{
			Code:    "PROGRAM_KSA_OCR_TIMEOUT",
			Message: fmt.Sprintf("OCR не успел распознать документ за %s", programImportTimeoutLabel(timeout)),
			Details: details,
		})
	case errors.Is(err, ocr.ErrNoText), errors.Is(err, ocr.ErrUnsupportedImage),
		errors.Is(err, fgos.ErrNoTextLayer):
		if !s.programOCR {
			details = append(details, "документ похож на скан: включите OCR_ENABLED=true, установите tesseract и poppler и повторите загрузку; инструкция: "+ocr.DocsPath)
		}
		writeJSON(w, http.StatusUnprocessableEntity, programImportErrorBody{
			Code:    "PROGRAM_KSA_INVALID",
			Message: "документ не содержит текста, из которого можно извлечь KSA",
			Details: details,
		})
	case errors.Is(err, fgos.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		writeErr(w, http.StatusServiceUnavailable, "PROGRAM_KSA_LLM_TIMEOUT",
			fmt.Sprintf("LLM-адаптер не успел извлечь KSA за %s", programImportTimeoutLabel(timeout)))
	case errors.Is(err, fgos.ErrNoClient), errors.Is(err, fgos.ErrKSAUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, programImportErrorBody{
			Code:    "PROGRAM_KSA_LLM_UNAVAILABLE",
			Message: "LLM-адаптер недоступен: проверьте настройки модели и повторите позже",
			Details: details,
		})
	case errors.Is(err, fgos.ErrNoKSA):
		writeJSON(w, http.StatusUnprocessableEntity, programImportErrorBody{
			Code:    "PROGRAM_KSA_NOT_FOUND",
			Message: "в документе не нашлось знаний, умений и навыков",
			Details: details,
		})
	case errors.Is(err, fgos.ErrUnsupportedFormat), errors.Is(err, fgos.ErrInvalidDocument):
		writeJSON(w, http.StatusUnprocessableEntity, programImportErrorBody{
			Code:    "PROGRAM_KSA_INVALID",
			Message: "документ ОПОП/РПД не прошёл извлечение",
			Details: details,
		})
	default:
		writeJSON(w, http.StatusServiceUnavailable, programImportErrorBody{
			Code:    "PROGRAM_KSA_EXTRACTION_UNAVAILABLE",
			Message: "не удалось извлечь KSA: повторите загрузку позже",
			Details: details,
		})
	}
}
