package api

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/ingestion"
)

// handleUploadSource принимает файл, извлекает из него текст и создаёт глобальный источник.
//
// Поддерживаются PDF, DOCX и текстовые форматы; содержимое не нужно вставлять вручную.
func (s *Server) handleUploadSource(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, ingestion.MaxDocumentSize+(1<<20))
	if err := r.ParseMultipartForm(ingestion.MaxDocumentSize + (1 << 20)); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "не удалось прочитать форму или файл больше 20 МБ")
		return
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
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "файл пустой")
		return
	}
	if len(data) > ingestion.MaxDocumentSize {
		writeErr(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "файл больше 20 МБ")
		return
	}

	content, err := ingestion.ExtractDocumentText(header.Filename, data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_DOCUMENT", err.Error())
		return
	}

	sourceType := domain.SourceType(strings.TrimSpace(r.FormValue("type")))
	switch sourceType {
	case domain.SourceVacancy, domain.SourceInterview, domain.SourceStandard, domain.SourceDocument:
	default:
		sourceType = domain.SourceDocument
	}

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		base := filepath.Base(header.Filename)
		title = strings.TrimSpace(strings.TrimSuffix(base, filepath.Ext(base)))
	}
	if title == "" {
		title = "Загруженный материал"
	}

	created, err := s.store.CreateSource(domain.Source{
		Type:    sourceType,
		Title:   title,
		Content: content,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось сохранить загруженный источник")
		return
	}
	if created == nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "источник не создан")
		return
	}
	// Индексируем в фоне: построение фрагментов и векторов может занять время,
	// а загрузка файла не должна ждать эмбеддинги.
	s.indexSourceAsync(created.ID)
	writeJSON(w, http.StatusCreated, created)
}
