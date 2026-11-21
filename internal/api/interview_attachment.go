package api

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/ingestion"
)

// handleInterviewAttachment accepts a PDF/DOCX/TXT evidence file and posts its
// text as the respondent's next message. The interviewer sees the evidence as
// part of the answer; the original file is not stored.
func (s *Server) handleInterviewAttachment(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleSpecialist, auth.RoleUser) {
		return
	}
	runID := r.PathValue("id")
	if !s.allowInterviewAccess(w, r, runID) {
		return
	}
	if s.chat == nil {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "чат недоступен")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, ingestion.MaxDocumentSize+(1<<20))
	if err := r.ParseMultipartForm(ingestion.MaxDocumentSize + (1 << 20)); err != nil {
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
	if err != nil || len(data) == 0 {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "не удалось прочитать файл")
		return
	}
	if len(data) > ingestion.MaxDocumentSize {
		writeErr(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "файл больше 20 МБ")
		return
	}
	text, err := ingestion.ExtractDocumentText(header.Filename, data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_DOCUMENT", err.Error())
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		writeErr(w, http.StatusBadRequest, "EMPTY_DOCUMENT", "в документе не найден текст")
		return
	}
	title := strings.TrimSpace(strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename)))
	if title == "" {
		title = "доказательство"
	}
	message, err := s.chat.Post(runID, "Прикрепляю доказательство «"+title+"»:\n\n"+truncateProfileText(text, 12000))
	if err != nil {
		writeErr(w, http.StatusConflict, "CHAT_UNAVAILABLE", "не удалось добавить доказательство в интервью")
		return
	}
	writeJSON(w, http.StatusCreated, message)
}
