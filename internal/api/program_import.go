package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/fgos"
	"activity-descriptor/internal/ingestion"
	"activity-descriptor/internal/ocr"
)

const (
	programDraftStatusDraft     = "draft"
	programDraftStatusPublished = "published"
	programDraftStatusRejected  = "rejected"

	programImportRequestLimit = ingestion.MaxDocumentSize + (1 << 20)
	programDecisionBodyLimit  = 1 << 20
	// defaultProgramImportTimeout — бюджет синхронного разбора PDF/DOCX/сканов,
	// если PROGRAM_IMPORT_TIMEOUT_S не задан. OCR и LLM работают в одном
	// бюджете: живой ФГОС на 10–15 страниц не укладывается в прежние 5 минут.
	defaultProgramImportTimeout = 15 * time.Minute
	// envProgramImportTimeout — переменная окружения с бюджетом импорта в
	// секундах (допускается и duration: 15m).
	envProgramImportTimeout = "PROGRAM_IMPORT_TIMEOUT_S"
)

// errProgramDraftDecided означает гонку: черновик уже опубликован или отклонён
// другим запросом. Хендлер отвечает на неё 409.
var errProgramDraftDecided = errors.New("PROGRAM_DRAFT_ALREADY_DECIDED")

// ProgramImportReport - сводка импорта, которая сохраняется в report_json и
// показывается администратору до публикации.
type ProgramImportReport struct {
	Competencies int      `json:"competencies"`
	KSA          int      `json:"ksa"`
	Warnings     []string `json:"warnings"`
}

// ProgramImportDraft - черновик импортированной программы. Program хранится в
// том виде, в котором его вернул ParseProgramFile.
type ProgramImportDraft struct {
	ID           string                     `json:"id"`
	Status       string                     `json:"status"`
	Key          string                     `json:"key"`
	SourceName   string                     `json:"source_name"`
	SourceFormat string                     `json:"source_format"`
	Program      ingestion.EducationProgram `json:"program"`
	Report       ProgramImportReport        `json:"report"`
	Warnings     []string                   `json:"warnings"`
	CreatedBy    string                     `json:"created_by,omitempty"`
	CreatedAt    time.Time                  `json:"created_at"`
	DecidedAt    *time.Time                 `json:"decided_at,omitempty"`
	DecidedBy    string                     `json:"decided_by,omitempty"`
	Error        string                     `json:"error,omitempty"`
}

// ProgramImportDraftSummary - строка списка черновиков.
type ProgramImportDraftSummary struct {
	ID         string              `json:"id"`
	Key        string              `json:"key"`
	SourceName string              `json:"source_name"`
	Status     string              `json:"status"`
	CreatedAt  time.Time           `json:"created_at"`
	Counts     ProgramImportCounts `json:"counts"`
}

type ProgramImportCounts struct {
	Competencies int `json:"competencies"`
	KSA          int `json:"ksa"`
	Warnings     int `json:"warnings"`
}

type programImportResult struct {
	DraftID string                     `json:"draft_id"`
	Program ingestion.EducationProgram `json:"program"`
	Report  ProgramImportReport        `json:"report"`
	Status  string                     `json:"status"`
}

type programPublishRequest struct {
	Taxonomy string `json:"taxonomy"`
}

type programRejectRequest struct {
	Reason string `json:"reason"`
}

// programDraftStore - хранилище черновиков. Его реализует *PGStore; в
// in-memory store такого хранилища нет, поэтому демо-режим отвечает 501.
type programDraftStore interface {
	CreateProgramImportDraft(ctx context.Context, draft ProgramImportDraft) (ProgramImportDraft, error)
	ListProgramImportDrafts(ctx context.Context) ([]ProgramImportDraftSummary, error)
	GetProgramImportDraft(ctx context.Context, id string) (ProgramImportDraft, error)
	// SaveProgramImportDraftProgram сохраняет program_json и счётчики отчёта
	// после добавления KSA из второго документа (ОПОП/РПД).
	SaveProgramImportDraftProgram(ctx context.Context, id string, program ingestion.EducationProgram, report ProgramImportReport) error
	MarkProgramImportDraftPublished(ctx context.Context, id, decidedBy string, report ingestion.EducationReport) error
	MarkProgramImportDraftRejected(ctx context.Context, id, decidedBy, reason string) error
}

// programPublisher отделяет хендлер от PostgreSQL: в тестах подменяется
// заглушкой, а в продакшене вызывает ingestion.SaveEducation.
type programPublisher interface {
	PublishEducation(ctx context.Context, key, taxonomy string, program ingestion.EducationProgram) (ingestion.EducationReport, error)
}

// programImportErrorBody расширяет обычный errorBody списком проблем строк.
type programImportErrorBody struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Details []string `json:"details"`
}

func (s *Server) programDrafts() (programDraftStore, bool) {
	if s == nil || s.store == nil {
		return nil, false
	}
	store, ok := s.store.(programDraftStore)
	return store, ok
}

func (s *Server) programPublisherForStore() programPublisher {
	if s == nil {
		return nil
	}
	if s.programPublisher != nil {
		return s.programPublisher
	}
	if publisher, ok := s.store.(programPublisher); ok {
		return publisher
	}
	return nil
}

// handleProgramImport godoc
// @Summary      Импортировать образовательную программу в черновик
// @Description  Принимает multipart-файл CSV/XLSX/JSON/TXT/MD, а также PDF/DOCX при настроенном LLM-адаптере ФГОС и PNG/JPEG при включённом OCR, и постоянный key программы. Файл разбирается и валидируется, но не публикуется: создаётся черновик для проверки человеком. PDF/DOCX и сканы разбираются синхронно через LLM и OCR (таймаут PROGRAM_IMPORT_TIMEOUT_S, по умолчанию 15 минут); страницы без текстового слоя распознаются tesseract, в предупреждениях появляется «страница N распознана OCR». Если адаптер не настроен, возвращается 422, если модель или OCR недоступны — 503. Ошибки строк возвращаются списком details.
// @Tags         academic
// @Accept       multipart/form-data
// @Produce      json
// @Param        file  formData  file    true   "Файл программы (CSV, XLSX, JSON, TXT, MD, PDF, DOCX, PNG или JPEG при OCR)"
// @Param        key   formData  string  true   "Постоянный код программы"
// @Success      201   {object}  programImportResult
// @Failure      400   {object}  errorBody
// @Failure      401   {object}  errorBody
// @Failure      403   {object}  errorBody
// @Failure      422   {object}  programImportErrorBody
// @Failure      500   {object}  errorBody
// @Failure      501   {object}  errorBody
// @Failure      503   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/admin/programs/import [post]
func (s *Server) handleProgramImport(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	drafts, ok := s.programDrafts()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "импорт образовательных программ требует PostgreSQL")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, programImportRequestLimit)
	if err := r.ParseMultipartForm(programImportRequestLimit); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "не удалось прочитать форму или файл больше 20 МБ")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	rawKey := strings.TrimSpace(r.FormValue("key"))
	if rawKey == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "нужно непустое поле key - постоянный код программы")
		return
	}
	if len([]rune(rawKey)) > 200 {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "key длиннее 200 символов")
		return
	}
	// Импорт не ломает старые ключи без «/», но предупреждает администратора в
	// отчёте: реестр источников такие ключи уже не принимает.
	key, keyWarning, _ := ingestion.NormalizeProgramKeyLenient(rawKey)

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
		sourceName = "program"
	}
	var program ingestion.EducationProgram
	var warnings []string
	format := documentFormat(sourceName)
	if isProgramDocumentFormat(format) {
		// PNG/JPEG имеют смысл только при OCR: без него это не программа, а
		// картинка. PDF/DOCX без OCR всё равно пробуются как текстовые.
		if isOCRImageFormat(format) && !s.programOCR {
			writeProgramOCRDisabled(w)
			return
		}
		var extracted bool
		program, warnings, extracted = s.importProgramDocument(w, r, sourceName, format, data)
		if !extracted {
			return
		}
	} else {
		program, warnings, err = ingestion.ParseProgramFile(sourceName, bytes.NewReader(data))
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, programImportErrorBody{
				Code:    "PROGRAM_IMPORT_INVALID",
				Message: "файл программы не прошёл проверку",
				Details: programImportProblems(err),
			})
			return
		}
	}
	warnings = nonNilStrings(warnings)
	if keyWarning != "" {
		warnings = append([]string{keyWarning}, warnings...)
	}
	// В форматах без колонки code (TXT/MD и часть JSON) постоянный key импорта
	// становится кодом программы, чтобы черновик был самодостаточным.
	if strings.TrimSpace(program.Code) == "" {
		program.Code = key
	}
	report := ProgramImportReport{
		Competencies: len(program.Competencies),
		KSA:          countProgramKSA(program),
		Warnings:     warnings,
	}
	created, err := drafts.CreateProgramImportDraft(r.Context(), ProgramImportDraft{
		Status:       programDraftStatusDraft,
		Key:          key,
		SourceName:   sourceName,
		SourceFormat: ingestion.ProgramSourceFormat(sourceName),
		Program:      program,
		Report:       report,
		Warnings:     warnings,
		CreatedBy:    auth.Subject(r.Context()),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось сохранить черновик программы")
		return
	}
	if created.Status == "" {
		created.Status = programDraftStatusDraft
	}
	created.Warnings = nonNilStrings(created.Warnings)
	created.Report.Warnings = nonNilStrings(created.Report.Warnings)
	writeJSON(w, http.StatusCreated, programImportResult{
		DraftID: created.ID,
		Program: created.Program,
		Report:  created.Report,
		Status:  created.Status,
	})
}

// documentFormat возвращает расширение файла без точки и без регистра.
func documentFormat(name string) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(strings.TrimSpace(name))), ".")
}

// importProgramDocument разбирает PDF/DOCX/PNG/JPEG через LLM-адаптер ФГОС. В
// отличие от CSV/XLSX здесь нет детерминированного парсера: программу и цитаты
// возвращает модель, а страницы без текстового слоя — OCR, поэтому статус
// ошибки зависит от причины. При ошибке хендлер уже записал ответ, и
// extracted=false.
func (s *Server) importProgramDocument(w http.ResponseWriter, r *http.Request, sourceName, format string, data []byte) (ingestion.EducationProgram, []string, bool) {
	if s == nil || s.programTextExtractor == nil {
		// 422, а не 501: запрос осмысленный, но сервер не умеет разбирать
		// документы без LLM. Администратор может загрузить CSV/XLSX.
		writeJSON(w, http.StatusUnprocessableEntity, programImportErrorBody{
			Code:    "PROGRAM_IMPORT_LLM_NOT_CONFIGURED",
			Message: "PDF, DOCX и сканы требуют LLM-адаптера ФГОС: задайте LLM_API_KEY на сервере или загрузите программу в CSV/XLSX/JSON",
			Details: []string{"без LLM нельзя извлечь компетенции и цитаты из PDF/DOCX/сканов"},
		})
		return ingestion.EducationProgram{}, nil, false
	}
	timeout := s.programImportTimeout()
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	pages, err := s.extractProgramPages(ctx, sourceName, data)
	if err != nil {
		s.writeProgramDocumentError(w, err, timeout)
		return ingestion.EducationProgram{}, nil, false
	}
	program, warnings, err := s.programTextExtractor.Extract(ctx, pages)
	if err != nil {
		s.writeProgramDocumentError(w, err, timeout)
		return ingestion.EducationProgram{}, nil, false
	}
	if strings.TrimSpace(program.Title) == "" {
		// Модель возвращает только компетенции, поэтому название программы
		// берём из имени файла: черновик должен быть читаемым до публикации.
		program.Title = programTitleFromFileName(sourceName)
	}
	if warning := fgos.ApproximatePageNumbersWarningFor(format); warning != "" {
		warnings = append([]string{warning}, warnings...)
	}
	return program, warnings, true
}

// extractProgramPages передаёт контекст HTTP-запроса, если адаптер его умеет:
// так общий таймаут импорта ограничивает и OCR, и извлечение компетенций.
// Старые реализации DocumentExtractor работают без контекста.
func (s *Server) extractProgramPages(ctx context.Context, sourceName string, data []byte) ([]fgos.Page, error) {
	if contextual, ok := s.programTextExtractor.(fgos.ContextDocumentExtractor); ok {
		return contextual.ExtractPagesContext(ctx, sourceName, data)
	}
	return s.programTextExtractor.ExtractPages(sourceName, data)
}

// programImportTimeout — бюджет синхронного импорта PDF/DOCX/сканов. OCR и
// LLM работают в одном бюджете; значение задаётся PROGRAM_IMPORT_TIMEOUT_S.
func (s *Server) programImportTimeout() time.Duration {
	if s != nil && s.programImportBudget > 0 {
		return s.programImportBudget
	}
	return defaultProgramImportTimeout
}

// ProgramImportTimeoutFromEnv — экспортированный доступ к бюджету импорта для
// cmd/server: тем же значением ограничивается fgos.Extractor.Timeout, иначе
// внутренний лимит адаптера сработает раньше HTTP-контекста.
func ProgramImportTimeoutFromEnv() time.Duration {
	return programImportTimeoutFromEnv()
}

// programImportTimeoutFromEnv читает PROGRAM_IMPORT_TIMEOUT_S в секундах (900)
// или duration (15m). Некорректное значение заменяется умолчанием, чтобы
// сервер не падал из-за опечатки в env.
func programImportTimeoutFromEnv() time.Duration {
	raw := strings.TrimSpace(os.Getenv(envProgramImportTimeout))
	if raw == "" {
		return defaultProgramImportTimeout
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if duration, err := time.ParseDuration(raw); err == nil && duration > 0 {
		return duration
	}
	return defaultProgramImportTimeout
}

// programImportTimeoutLabel переводит таймаут в текст ошибки («15 минут»).
func programImportTimeoutLabel(timeout time.Duration) string {
	minutes := int(timeout / time.Minute)
	if minutes <= 0 {
		return timeout.String()
	}
	return fmt.Sprintf("%d минут", minutes)
}

// writeProgramOCRDisabled объясняет, что изображение нельзя разобрать без OCR.
func writeProgramOCRDisabled(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnprocessableEntity, programImportErrorBody{
		Code:    "PROGRAM_IMPORT_OCR_DISABLED",
		Message: "изображение программы требует OCR: задайте OCR_ENABLED=true на сервере и установите tesseract/poppler или загрузите PDF с текстовым слоем",
		Details: []string{"включение OCR и установка инструментов: " + ocr.DocsPath},
	})
}

// writeProgramDocumentError переводит ошибку адаптера в HTTP-ответ: 422 — файл
// или содержимое не подходят, 503 — модель или OCR недоступны/не успели.
func (s *Server) writeProgramDocumentError(w http.ResponseWriter, err error, timeout time.Duration) {
	switch {
	case errors.Is(err, ocr.ErrDisabled):
		writeJSON(w, http.StatusUnprocessableEntity, programImportErrorBody{
			Code:    "PROGRAM_IMPORT_OCR_DISABLED",
			Message: "документ является сканом: OCR на сервере выключен",
			Details: programImportProblems(err),
		})
	case errors.Is(err, ocr.ErrUnavailable), errors.Is(err, ocr.ErrToolNotFound):
		writeJSON(w, http.StatusServiceUnavailable, programImportErrorBody{
			Code:    "PROGRAM_IMPORT_OCR_UNAVAILABLE",
			Message: "OCR недоступен: установите tesseract и poppler на сервере или повторите позже",
			Details: programImportProblems(err),
		})
	case errors.Is(err, ocr.ErrPageLimit):
		writeJSON(w, http.StatusUnprocessableEntity, programImportErrorBody{
			Code:    "PROGRAM_IMPORT_OCR_PAGE_LIMIT",
			Message: "скан слишком большой для OCR: разделите документ или увеличьте OCR_MAX_PAGES",
			Details: programImportProblems(err),
		})
	case errors.Is(err, ocr.ErrTimeout):
		writeJSON(w, http.StatusServiceUnavailable, programImportErrorBody{
			Code:    "PROGRAM_IMPORT_OCR_TIMEOUT",
			Message: fmt.Sprintf("OCR не успел распознать документ за %s", programImportTimeoutLabel(timeout)),
			Details: programImportProblems(err),
		})
	case errors.Is(err, ocr.ErrNoText), errors.Is(err, ocr.ErrUnsupportedImage):
		writeJSON(w, http.StatusUnprocessableEntity, programImportErrorBody{
			Code:    "PROGRAM_IMPORT_INVALID",
			Message: "документ программы не прошёл извлечение",
			Details: programImportProblems(err),
		})
	case errors.Is(err, fgos.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		writeErr(w, http.StatusServiceUnavailable, "PROGRAM_IMPORT_LLM_TIMEOUT",
			fmt.Sprintf("LLM-адаптер не успел разобрать документ за %s", programImportTimeoutLabel(timeout)))
	case errors.Is(err, fgos.ErrNoTextLayer),
		errors.Is(err, fgos.ErrNoCompetencies),
		errors.Is(err, fgos.ErrUnsupportedFormat),
		errors.Is(err, fgos.ErrInvalidDocument):
		details := programImportProblems(err)
		if !s.programOCR {
			details = append(details, "документ похож на скан: включите OCR_ENABLED=true, установите tesseract и poppler и повторите импорт; инструкция: "+ocr.DocsPath)
		}
		writeJSON(w, http.StatusUnprocessableEntity, programImportErrorBody{
			Code:    "PROGRAM_IMPORT_INVALID",
			Message: "документ программы не прошёл извлечение",
			Details: details,
		})
	default:
		writeJSON(w, http.StatusServiceUnavailable, programImportErrorBody{
			Code:    "PROGRAM_IMPORT_LLM_UNAVAILABLE",
			Message: "LLM-адаптер недоступен: проверьте настройки модели и повторите позже",
			Details: programImportProblems(err),
		})
	}
}

// programTitleFromFileName — запасное название черновика, если LLM не вернул
// title программы.
func programTitleFromFileName(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	base = strings.TrimSpace(strings.TrimSuffix(base, filepath.Ext(base)))
	if base == "" || base == "." {
		return "Образовательная программа"
	}
	return base
}

// isProgramDocumentFormat сообщает, разбирается ли файл LLM-адаптером ФГОС
// (PDF/DOCX и изображения при включённом OCR).
func isProgramDocumentFormat(format string) bool {
	switch format {
	case "pdf", "docx":
		return true
	default:
		return isOCRImageFormat(format)
	}
}

// isOCRImageFormat — форматы изображений, которые распознаёт OCR.
func isOCRImageFormat(format string) bool {
	switch format {
	case "png", "jpg", "jpeg":
		return true
	default:
		return false
	}
}

// handleListProgramDrafts godoc
// @Summary      Список черновиков импорта образовательных программ
// @Description  Возвращает черновики в порядке создания (новые первыми) со счётчиками компетенций, KSA и предупреждений.
// @Tags         academic
// @Produce      json
// @Success      200  {array}   ProgramImportDraftSummary
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/admin/programs/drafts [get]
func (s *Server) handleListProgramDrafts(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	drafts, ok := s.programDrafts()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "список черновиков программ требует PostgreSQL")
		return
	}
	items, err := drafts.ListProgramImportDrafts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось загрузить черновики программ")
		return
	}
	if items == nil {
		items = []ProgramImportDraftSummary{}
	}
	writeJSON(w, http.StatusOK, items)
}

// handleGetProgramDraft godoc
// @Summary      Черновик импорта образовательной программы
// @Description  Возвращает черновик вместе с разобранной программой и отчётом импорта.
// @Tags         academic
// @Produce      json
// @Param        id   path      string  true  "Draft ID"
// @Success      200  {object}  ProgramImportDraft
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/admin/programs/drafts/{id} [get]
func (s *Server) handleGetProgramDraft(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	drafts, ok := s.programDrafts()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "черновики программ требуют PostgreSQL")
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
	if draft.Warnings == nil {
		draft.Warnings = []string{}
	}
	draft.Report.Warnings = nonNilStrings(draft.Report.Warnings)
	writeJSON(w, http.StatusOK, draft)
}

// handlePublishProgramDraft godoc
// @Summary      Опубликовать черновик образовательной программы
// @Description  Повторно разбирает сохранённую программу и сохраняет её через ingestion.SaveEducation. taxonomy - необязательный UUID справочника для сопоставления кодов. Повторная публикация или публикация отклонённого черновика даёт 409.
// @Tags         academic
// @Accept       json
// @Produce      json
// @Param        id    path      string                  true  "Draft ID"
// @Param        body  body      programPublishRequest   false "Необязательная таксономия"
// @Success      200   {object}  EducationReport
// @Failure      400   {object}  errorBody
// @Failure      401   {object}  errorBody
// @Failure      403   {object}  errorBody
// @Failure      404   {object}  errorBody
// @Failure      409   {object}  errorBody
// @Failure      500   {object}  errorBody
// @Failure      501   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/admin/programs/drafts/{id}/publish [post]
func (s *Server) handlePublishProgramDraft(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	drafts, ok := s.programDrafts()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "публикация программ требует PostgreSQL")
		return
	}
	publisher := s.programPublisherForStore()
	if publisher == nil {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "публикация программ недоступна: хранилище не умеет сохранять программы")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "не указан идентификатор черновика")
		return
	}
	var request programPublishRequest
	if err := decodeProgramDecisionBody(w, r, &request); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "некорректный JSON запроса")
		return
	}
	taxonomy := strings.TrimSpace(request.Taxonomy)
	if taxonomy != "" && !validUUID(taxonomy) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "taxonomy должен быть UUID")
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
	if draft.Status != programDraftStatusDraft {
		writeErr(w, http.StatusConflict, "PROGRAM_DRAFT_ALREADY_DECIDED", "черновик уже опубликован или отклонён")
		return
	}

	report, err := publisher.PublishEducation(r.Context(), draft.Key, taxonomy, draft.Program)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "PROGRAM_PUBLISH_FAILED", "не удалось опубликовать программу")
		return
	}
	if err = drafts.MarkProgramImportDraftPublished(r.Context(), id, auth.Subject(r.Context()), report); err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "черновик программы не найден")
		case errors.Is(err, errProgramDraftDecided):
			writeErr(w, http.StatusConflict, "PROGRAM_DRAFT_ALREADY_DECIDED", "черновик уже опубликован или отклонён")
		default:
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось зафиксировать публикацию программы")
		}
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// handleRejectProgramDraft godoc
// @Summary      Отклонить черновик образовательной программы
// @Description  Помечает черновик отклонённым; необязательное поле reason сохраняется в error. Повторное решение даёт 409.
// @Tags         academic
// @Accept       json
// @Param        id    path  string                true  "Draft ID"
// @Param        body  body  programRejectRequest  false "Необязательная причина"
// @Success      204
// @Failure      400  {object}  errorBody
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      409  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/admin/programs/drafts/{id}/reject [post]
func (s *Server) handleRejectProgramDraft(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	drafts, ok := s.programDrafts()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "отклонение программ требует PostgreSQL")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "не указан идентификатор черновика")
		return
	}
	var request programRejectRequest
	if err := decodeProgramDecisionBody(w, r, &request); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "некорректный JSON запроса")
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
	if draft.Status != programDraftStatusDraft {
		writeErr(w, http.StatusConflict, "PROGRAM_DRAFT_ALREADY_DECIDED", "черновик уже опубликован или отклонён")
		return
	}
	if err = drafts.MarkProgramImportDraftRejected(r.Context(), id, auth.Subject(r.Context()), strings.TrimSpace(request.Reason)); err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "черновик программы не найден")
		case errors.Is(err, errProgramDraftDecided):
			writeErr(w, http.StatusConflict, "PROGRAM_DRAFT_ALREADY_DECIDED", "черновик уже опубликован или отклонён")
		default:
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось отклонить черновик программы")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeProgramDecisionBody(w http.ResponseWriter, r *http.Request, target any) error {
	if r.Body == nil {
		return nil
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, programDecisionBodyLimit))
	if err != nil {
		return err
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("лишние данные после JSON")
	}
	return nil
}

func programImportProblems(err error) []string {
	var problems *ingestion.ProgramFileErrors
	if errors.As(err, &problems) {
		return problems.Details()
	}
	return []string{err.Error()}
}

func countProgramKSA(program ingestion.EducationProgram) int {
	count := 0
	for _, competency := range program.Competencies {
		count += len(competency.KSA)
	}
	return count
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
