package api

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/ingestion"
)

var hhResumeIDPattern = regexp.MustCompile(`[A-Za-z0-9]{6,}`)
var htmlTagPattern = regexp.MustCompile(`<[^>]+>`)
var titlePattern = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// handleImportResume parses a PDF/DOCX/TXT resume and returns its text for the
// interview profile. The file is not persisted as a global source.
func (s *Server) handleImportResume(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleSpecialist, auth.RoleUser) {
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
	writeJSON(w, http.StatusOK, map[string]any{
		"source":   "resume",
		"filename": filepath.Base(header.Filename),
		"text":     truncateProfileText(text, 12000),
	})
}

type hhProfileRequest struct {
	URL string `json:"url"`
}

// handleImportHHProfile fetches a public HH resume page and returns its text.
// It is best-effort: HH can require a captcha or block anonymous requests.
func (s *Server) handleImportHHProfile(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleSpecialist, auth.RoleUser) {
		return
	}
	var req hhProfileRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "некорректный JSON")
		return
	}
	raw := strings.TrimSpace(req.URL)
	id := resumeID(raw)
	if id == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "укажите ссылку на резюме HH")
		return
	}
	profileURL := "https://hh.ru/resume/" + id
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, profileURL, nil)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "HH_UNAVAILABLE", "не удалось запросить HH")
		return
	}
	request.Header.Set("User-Agent", envOr("HH_USER_AGENT", "ModeusHackathon/1.0 (contact@example.org)"))
	request.Header.Set("Accept-Language", "ru-RU,ru;q=0.9")
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "HH_UNAVAILABLE", "не удалось получить профиль HH")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeErr(w, http.StatusBadGateway, "HH_UNAVAILABLE", fmt.Sprintf("HH ответил HTTP %d", response.StatusCode))
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "HH_UNAVAILABLE", "не удалось прочитать профиль HH")
		return
	}
	document := string(body)
	title := "Профиль HH"
	if match := titlePattern.FindStringSubmatch(document); len(match) == 2 {
		if value := strings.TrimSpace(html.UnescapeString(match[1])); value != "" {
			title = value
		}
	}
	text := html.UnescapeString(htmlTagPattern.ReplaceAllString(document, " "))
	text = strings.Join(strings.Fields(text), " ")
	if len(text) < 80 {
		writeErr(w, http.StatusBadGateway, "HH_UNAVAILABLE", "HH не вернул текст резюме")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source": "hh",
		"url":    profileURL,
		"title":  title,
		"text":   truncateProfileText(text, 12000),
	})
}

func resumeID(raw string) string {
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil || !(strings.EqualFold(parsed.Host, "hh.ru") || strings.HasSuffix(strings.ToLower(parsed.Host), ".hh.ru")) {
			return ""
		}
		raw = strings.TrimPrefix(parsed.Path, "/resume/")
	}
	raw = strings.TrimSpace(strings.Split(raw, "?")[0])
	if hhResumeIDPattern.MatchString(raw) {
		return hhResumeIDPattern.FindString(raw)
	}
	return ""
}

func truncateProfileText(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
