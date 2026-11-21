package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/ingestion"
	"activity-descriptor/internal/logging"
	"activity-descriptor/internal/programwatch"
)

// programSourceCheckLease — lease ручной проверки. Он больше таймаута HTTP-
// запроса и разбора документа, но если процесс упал, источник освободится
// автоматически через 5 минут.
const programSourceCheckLease = 5 * time.Minute

// programSourceReleaseTimeout ограничивает освобождение блокировки при ошибке.
const programSourceReleaseTimeout = 5 * time.Second

type programSourceCreateRequest struct {
	ProgramKey    string `json:"program_key"`
	URL           string `json:"url"`
	IntervalHours *int   `json:"interval_hours"`
	DocFormat     string `json:"doc_format"`
}

type programSourceUpdateRequest struct {
	Enabled       *bool `json:"enabled"`
	IntervalHours *int  `json:"interval_hours"`
}

// programWatchStore возвращает реестр источников. In-memory store его не
// реализует, поэтому демо-режим отвечает 501.
func (s *Server) programWatchStore() (programwatch.Store, bool) {
	if s == nil || s.store == nil {
		return nil, false
	}
	store, ok := s.store.(programwatch.Store)
	return store, ok
}

func (s *Server) programWatchDrafts() (programwatch.DraftCreator, bool) {
	if s == nil || s.store == nil {
		return nil, false
	}
	drafts, ok := s.store.(programwatch.DraftCreator)
	return drafts, ok
}

// handleCreateProgramSource godoc
// @Summary      Добавить источник образовательной программы
// @Description  Регистрирует URL для регулярной проверки. program_key обязан быть каноническим: org/code/year/form. Разрешены только http/https URL.
// @Tags         academic
// @Accept       json
// @Produce      json
// @Param        body  body      programSourceCreateRequest  true  "Источник"
// @Success      201   {object}  programwatch.Source
// @Failure      400   {object}  errorBody
// @Failure      401   {object}  errorBody
// @Failure      403   {object}  errorBody
// @Failure      409   {object}  errorBody
// @Failure      500   {object}  errorBody
// @Failure      501   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/admin/program-sources [post]
func (s *Server) handleCreateProgramSource(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	store, ok := s.programWatchStore()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "реестр источников программ требует PostgreSQL")
		return
	}
	var request programSourceCreateRequest
	if err := decodeProgramDecisionBody(w, r, &request); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "некорректный JSON запроса")
		return
	}
	key, err := ingestion.NormalizeProgramKey(request.ProgramKey)
	if err != nil {
		message := "некорректный program_key: " + err.Error()
		if !strings.Contains(message, ingestion.ProgramKeyFormatExample) {
			message += "; пример: " + ingestion.ProgramKeyFormatExample
		}
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", message)
		return
	}
	interval := 24
	if request.IntervalHours != nil {
		interval = *request.IntervalHours
		if interval < 1 || interval > 720 {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "interval_hours должен быть от 1 до 720")
			return
		}
	}
	input, err := programwatch.NormalizeCreateInput(programwatch.CreateInput{
		ProgramKey:    key,
		URL:           request.URL,
		DocFormat:     request.DocFormat,
		IntervalHours: interval,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	created, err := store.CreateWatchSource(r.Context(), input)
	if err != nil {
		writeProgramWatchError(w, err, "не удалось добавить источник программы")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// handleListProgramSources godoc
// @Summary      Список источников образовательных программ
// @Description  Возвращает реестр URL, которые воркер проверяет по расписанию.
// @Tags         academic
// @Produce      json
// @Success      200  {array}   programwatch.Source
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/admin/program-sources [get]
func (s *Server) handleListProgramSources(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	store, ok := s.programWatchStore()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "реестр источников программ требует PostgreSQL")
		return
	}
	items, err := store.ListWatchSources(r.Context())
	if err != nil {
		writeProgramWatchError(w, err, "не удалось загрузить источники программ")
		return
	}
	if items == nil {
		items = []programwatch.Source{}
	}
	writeJSON(w, http.StatusOK, items)
}

// handleUpdateProgramSource godoc
// @Summary      Обновить источник образовательной программы
// @Description  Меняет enabled и/или interval_hours. Остальные поля неизменяемы: URL и ключ задаются при создании.
// @Tags         academic
// @Accept       json
// @Produce      json
// @Param        id    path      string                      true  "Program source ID"
// @Param        body  body      programSourceUpdateRequest  true  "Изменения"
// @Success      200   {object}  programwatch.Source
// @Failure      400   {object}  errorBody
// @Failure      401   {object}  errorBody
// @Failure      403   {object}  errorBody
// @Failure      404   {object}  errorBody
// @Failure      500   {object}  errorBody
// @Failure      501   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/admin/program-sources/{id} [patch]
func (s *Server) handleUpdateProgramSource(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	store, ok := s.programWatchStore()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "реестр источников программ требует PostgreSQL")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "не указан идентификатор источника")
		return
	}
	var request programSourceUpdateRequest
	if err := decodeProgramDecisionBody(w, r, &request); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "некорректный JSON запроса")
		return
	}
	patch := programwatch.SourcePatch{Enabled: request.Enabled, IntervalHours: request.IntervalHours}
	if err := programwatch.ValidatePatch(patch); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	updated, err := store.UpdateWatchSource(r.Context(), id, patch)
	if err != nil {
		writeProgramWatchError(w, err, "не удалось обновить источник программы")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleDeleteProgramSource godoc
// @Summary      Удалить источник образовательной программы
// @Description  Удаляет URL из реестра; уже созданные черновики не затрагиваются.
// @Tags         academic
// @Param        id   path  string  true  "Program source ID"
// @Success      204
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/admin/program-sources/{id} [delete]
func (s *Server) handleDeleteProgramSource(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	store, ok := s.programWatchStore()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "реестр источников программ требует PostgreSQL")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "не указан идентификатор источника")
		return
	}
	if err := store.DeleteWatchSource(r.Context(), id); err != nil {
		writeProgramWatchError(w, err, "не удалось удалить источник программы")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleCheckProgramSource godoc
// @Summary      Проверить источник образовательной программы синхронно
// @Description  Скачивает источник, учитывает ETag/Last-Modified/хэш, при изменении разбирает документ и создаёт черновик. Результат сохраняется в реестре. Пока идёт проверка, источник защищён lease-блокировкой: параллельный запрос или воркер получает 409 PROGRAM_SOURCE_BUSY.
// @Tags         academic
// @Produce      json
// @Param        id   path      string  true  "Program source ID"
// @Success      200  {object}  programwatch.CheckResult
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      409  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/admin/program-sources/{id}/check [post]
func (s *Server) handleCheckProgramSource(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	store, ok := s.programWatchStore()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "проверка источников программ требует PostgreSQL")
		return
	}
	drafts, ok := s.programWatchDrafts()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "создание черновиков программ требует PostgreSQL")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "не указан идентификатор источника")
		return
	}
	owner := programwatch.NewOwner("api")
	// С LLM-адаптером ручная проверка PDF/DOCX может занять до пяти минут:
	// lease должен перекрывать этот срок, иначе воркер перехватит источник и
	// запустит второй разбор.
	lease := programSourceCheckLease
	if s.programTextExtractor != nil {
		lease = programImportLLMTimeout + time.Minute
	}
	source, err := store.ClaimWatchSource(r.Context(), id, lease, owner)
	if err != nil {
		writeProgramWatchError(w, err, "не удалось проверить источник программы")
		return
	}
	logging.L().Info("program_watch_check_claimed",
		"source_id", id, "owner", owner, "lease", lease.String())
	// До успешного SaveWatchCheck блокировка наша: при ошибке или панике
	// освобождаем её, чтобы не ждать истечения lease.
	checked := false
	defer func() {
		if checked {
			return
		}
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), programSourceReleaseTimeout)
		defer cancel()
		if releaseErr := store.ReleaseWatchSource(releaseCtx, id, owner); releaseErr != nil {
			logging.L().Warn("program_watch_release_failed",
				"source_id", id, "owner", owner, "error", releaseErr)
		}
	}()
	checker := programwatch.NewChecker(store, drafts)
	checker.Logger = logging.L()
	// Тот же адаптер, что и у импорта: иначе «Проверить сейчас» для PDF/DOCX
	// всегда возвращал бы needs_parser при настроенном LLM.
	checker.PDFExtractor = s.programTextExtractor
	result, err := checker.CheckAndSave(r.Context(), source, owner)
	if err != nil {
		writeProgramWatchError(w, err, "не удалось проверить источник программы")
		return
	}
	// SaveWatchCheck снял блокировку вместе с сохранением результата.
	checked = true
	writeJSON(w, http.StatusOK, result)
}

func writeProgramWatchError(w http.ResponseWriter, err error, message string) {
	switch {
	case errors.Is(err, programwatch.ErrNotFound):
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "источник программы не найден")
	case errors.Is(err, programwatch.ErrWatchSourceBusy):
		writeErr(w, http.StatusConflict, "PROGRAM_SOURCE_BUSY", "источник уже проверяется")
	case errors.Is(err, programwatch.ErrDuplicate):
		writeErr(w, http.StatusConflict, "PROGRAM_SOURCE_ALREADY_EXISTS", "источник с таким program_key и url уже существует")
	case errors.Is(err, programwatch.ErrUnavailable):
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "реестр источников программ требует PostgreSQL")
	default:
		writeErr(w, http.StatusInternalServerError, "INTERNAL", message)
	}
}
