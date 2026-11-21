package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/programdocx"
)

// handleExportProgramDraftDOCX godoc
// @Summary      Выгрузить черновик образовательной программы в DOCX
// @Description  Только admin. Собирает читабельный DOCX: название и реквизиты программы, сводку, таблицу компетенций с знаниями/умениями/навыками, предупреждения импорта и подпись по статусу черновика. Имя файла — program-<безопасный код>.docx, в нём остаются только [a-z0-9._-].
// @Tags         academic
// @Produce      application/vnd.openxmlformats-officedocument.wordprocessingml.document
// @Param        id   path  string  true  "Draft ID"
// @Success      200  {file}  file  "DOCX-документ"
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/admin/programs/drafts/{id}/export.docx [get]
func (s *Server) handleExportProgramDraftDOCX(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	drafts, ok := s.programDrafts()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "экспорт черновиков программ требует PostgreSQL")
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

	code := strings.TrimSpace(draft.Program.Code)
	if code == "" {
		code = draft.Key
	}
	document, err := programdocx.Build(programdocx.Draft{
		Key:        draft.Key,
		Status:     draft.Status,
		SourceName: draft.SourceName,
		CreatedAt:  draft.CreatedAt,
		Warnings:   draft.Warnings,
		Program:    draft.Program,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось собрать DOCX черновика программы")
		return
	}

	filename := programdocx.FileName(code)
	w.Header().Set("Content-Type", programdocx.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(document)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(document)
}
