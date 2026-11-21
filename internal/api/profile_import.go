package api

import (
	"context"
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

var (
	hhProfileBaseURL = "https://hh.ru/resume/"
	hhAPIBaseURL     = "https://api.hh.ru/resumes/"
)

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
	writeJSON(w, http.StatusOK, map[string]any{
		"source":   "resume",
		"filename": filepath.Base(header.Filename),
		"text":     truncateProfileText(text, 12000),
	})
}

type hhProfileRequest struct {
	URL string `json:"url"`
}

// handleImportHHProfile fetches a public HH resume. When HH_ACCESS_TOKEN is
// configured, the official API is tried first; otherwise the public page is
// parsed. Both paths are best-effort because HH may require a captcha.
func (s *Server) handleImportHHProfile(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleSpecialist, auth.RoleUser) {
		return
	}
	var req hhProfileRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "некорректный JSON")
		return
	}
	id := resumeID(strings.TrimSpace(req.URL))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "укажите ссылку на резюме HH")
		return
	}
	title, text, profileURL, err := fetchHH(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "HH_UNAVAILABLE", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source": "hh",
		"url":    profileURL,
		"title":  title,
		"text":   truncateProfileText(text, 12000),
	})
}

func fetchHH(ctx context.Context, id string) (string, string, string, error) {
	profileURL := hhProfileBaseURL + id
	if token := strings.TrimSpace(os.Getenv("HH_ACCESS_TOKEN")); token != "" {
		if title, text, err := fetchHHAPI(ctx, id, token); err == nil {
			return title, text, profileURL, nil
		}
	}
	title, text, err := fetchHHPage(ctx, profileURL)
	if err != nil {
		return "", "", profileURL, err
	}
	return title, text, profileURL, nil
}

func fetchHHAPI(ctx context.Context, id, token string) (string, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, hhAPIBaseURL+url.PathEscape(id), nil)
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("User-Agent", envOr("HH_USER_AGENT", "ModeusHackathon/1.0 (contact@example.org)"))
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("HH API HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return "", "", err
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", "", err
	}
	title := flattenString(payload["title"])
	text := flattenHHJSON(payload)
	if text == "" {
		return "", "", fmt.Errorf("HH API вернул пустое резюме")
	}
	return title, text, nil
}

func fetchHHPage(ctx context.Context, profileURL string) (string, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, profileURL, nil)
	if err != nil {
		return "", "", err
	}
	request.Header.Set("User-Agent", envOr("HH_USER_AGENT", "ModeusHackathon/1.0 (contact@example.org)"))
	request.Header.Set("Accept-Language", "ru-RU,ru;q=0.9")
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("не удалось получить профиль HH")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("HH ответил HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return "", "", fmt.Errorf("не удалось прочитать профиль HH")
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
		return "", "", fmt.Errorf("HH не вернул текст резюме")
	}
	return title, text, nil
}

func flattenHHJSON(value any) string {
	parts := make([]string, 0, 32)
	walkHHJSON(value, &parts)
	return strings.Join(parts, " ")
}

func walkHHJSON(value any, parts *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			_ = key
			walkHHJSON(item, parts)
		}
	case []any:
		for _, item := range typed {
			walkHHJSON(item, parts)
		}
	case string:
		value := strings.TrimSpace(typed)
		if value != "" && !strings.HasPrefix(value, "http") {
			*parts = append(*parts, value)
		}
	case float64, bool:
		// Numeric ids and flags are not useful resume text.
	}
}

func flattenString(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
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
