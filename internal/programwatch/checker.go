package programwatch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"activity-descriptor/internal/ingestion"
)

const (
	defaultMaxDocumentBytes int64 = 20 << 20
	defaultHTTPTimeout            = 60 * time.Second
	maxStoredErrorRunes           = 1000
	maxBackoff                    = 30 * 24 * time.Hour
	defaultUserAgent              = "program-watcher/1.0"
)

var htmlHrefPattern = regexp.MustCompile(`(?is)href\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)

var watchedDocumentExtensions = map[string]bool{
	".xlsx": true,
	".csv":  true,
	".json": true,
	".txt":  true,
}

// Checker проверяет источники и сохраняет результат. Зависимости — интерфейсы,
// поэтому и воркер, и API, и тесты работают без PostgreSQL и без сети.
type Checker struct {
	Store            Store
	Drafts           DraftCreator
	Client           *http.Client
	Now              func() time.Time
	MaxDocumentBytes int64
	UserAgent        string
	Logger           *slog.Logger
}

// NewChecker создаёт чекер с боевыми настройками по умолчанию.
func NewChecker(store Store, drafts DraftCreator) *Checker {
	return &Checker{Store: store, Drafts: drafts}
}

// CheckAndSave синхронно проверяет один источник, применяет экспоненциальную
// паузу при ошибке и сохраняет статус/валидаторы/черновик.
func (c *Checker) CheckAndSave(ctx context.Context, source Source) (CheckResult, error) {
	now := c.now()
	result, update := c.check(ctx, source)
	update.CheckedAt = now
	if result.Status == StatusError {
		update.FailureCount = source.FailureCount + 1
		// last_checked_at сдвигается так, чтобы due-условие
		// last_checked_at + interval_hours <= now сработало через
		// interval_hours * 2^(failure_count-1), но не позже 30 дней.
		update.CheckedAt = pauseUntil(now, source.IntervalHours, update.FailureCount)
	} else {
		update.FailureCount = 0
	}
	if err := c.Store.SaveWatchCheck(ctx, source.ID, update); err != nil {
		return result, err
	}
	return result, nil
}

func (c *Checker) check(ctx context.Context, source Source) (CheckResult, CheckUpdate) {
	update := CheckUpdate{}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return errorResult(fmt.Sprintf("некорректный url источника: %v", err))
	}
	request.Header.Set("User-Agent", c.userAgent())
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/json,text/csv,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet,text/plain,*/*")
	if source.ETag != "" {
		request.Header.Set("If-None-Match", source.ETag)
	}
	if source.LastModified != "" {
		request.Header.Set("If-Modified-Since", source.LastModified)
	}
	response, err := c.client().Do(request)
	if err != nil {
		return errorResult(fmt.Sprintf("источник недоступен: %v", err))
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusNotModified:
		return CheckResult{Status: StatusUnchanged}, update
	case http.StatusOK:
	default:
		return errorResult(fmt.Sprintf("источник ответил HTTP %d", response.StatusCode))
	}

	body, err := readLimited(response.Body, c.maxDocumentBytes())
	if err != nil {
		return errorResult(fmt.Sprintf("не удалось прочитать ответ: %v", err))
	}
	bodyHash := sha256Hex(body)
	etag := response.Header.Get("ETag")
	lastModified := response.Header.Get("Last-Modified")
	contentType := response.Header.Get("Content-Type")

	setValidators := func(hash string) {
		update.ETag = &etag
		update.LastModified = &lastModified
		update.LastHash = &hash
	}

	if source.LastHash != "" && bodyHash == source.LastHash {
		setValidators(bodyHash)
		return CheckResult{Status: StatusUnchanged}, update
	}

	documentBody := body
	documentName := fileNameFromURL(source.URL)
	documentContentType := contentType

	if isHTML(contentType, body) {
		documentURL, ok := firstDocumentLink(body, response.Request.URL)
		if !ok {
			return CheckResult{Status: StatusNeedsAttention, Error: "на странице нет ссылки на документ"}, update
		}
		fetched, name, fetchedType, fetchErr := c.fetchDocument(ctx, documentURL)
		if fetchErr != nil {
			return errorResult(fetchErr.Error())
		}
		documentBody, documentName, documentContentType = fetched, name, fetchedType
	}

	format := resolveDocumentFormat(source.DocFormat, documentName, documentContentType)
	switch format {
	case "pdf", "docx":
		return CheckResult{Status: StatusNeedsParser, Error: "PDF и DOCX требуют LLM-адаптера"}, update
	case "html":
		return errorResult("источник вернул HTML вместо документа программы")
	}
	if !isParsedProgramFormat(format) {
		return CheckResult{Status: StatusNeedsParser, Error: fmt.Sprintf("формат %q не поддерживается парсером программ", format)}, update
	}
	if c.Drafts == nil {
		return errorResult("создатель черновиков не настроен")
	}

	parserName := parserFileName(documentName, format)
	program, warnings, parseErr := ingestion.ParseProgramFile(parserName, bytes.NewReader(documentBody))
	if parseErr != nil {
		return errorResult("не удалось разобрать документ: " + compactError(parseErr.Error()))
	}
	if strings.TrimSpace(program.Code) == "" {
		program.Code = source.ProgramKey
	}
	if warnings == nil {
		warnings = []string{}
	}
	draftID, draftErr := c.Drafts.CreateProgramWatchDraft(ctx, DraftInput{
		ProgramKey:   source.ProgramKey,
		SourceName:   documentName,
		SourceFormat: ingestion.ProgramSourceFormat(parserName),
		Program:      program,
		Warnings:     warnings,
		Competencies: len(program.Competencies),
		KSA:          countKSA(program),
	})
	if draftErr != nil {
		return errorResult("не удалось создать черновик: " + compactError(draftErr.Error()))
	}
	setValidators(bodyHash)
	update.DraftID = draftID
	return CheckResult{Status: StatusChanged, DraftID: draftID}, update
}

func (c *Checker) fetchDocument(ctx context.Context, rawURL string) ([]byte, string, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", "", fmt.Errorf("некорректная ссылка на документ: %w", err)
	}
	request.Header.Set("User-Agent", c.userAgent())
	response, err := c.client().Do(request)
	if err != nil {
		return nil, "", "", fmt.Errorf("не удалось скачать документ: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", "", fmt.Errorf("документ ответил HTTP %d", response.StatusCode)
	}
	body, err := readLimited(response.Body, c.maxDocumentBytes())
	if err != nil {
		return nil, "", "", fmt.Errorf("не удалось прочитать документ: %w", err)
	}
	name := fileNameFromURL(rawURL)
	contentType := response.Header.Get("Content-Type")
	return body, name, contentType, nil
}

func (c *Checker) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: defaultHTTPTimeout}
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

func (c *Checker) maxDocumentBytes() int64 {
	if c.MaxDocumentBytes > 0 {
		return c.MaxDocumentBytes
	}
	return defaultMaxDocumentBytes
}

func (c *Checker) userAgent() string {
	if strings.TrimSpace(c.UserAgent) != "" {
		return c.UserAgent
	}
	return defaultUserAgent
}

func (c *Checker) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

// RunStats — сводка одного цикла.
type RunStats struct {
	Fetched        int
	Changed        int
	Unchanged      int
	NeedsParser    int
	NeedsAttention int
	Errors         int
}

// RunOnce проверяет не больше limit просроченных источников последовательно.
func (c *Checker) RunOnce(ctx context.Context, limit int) (RunStats, error) {
	stats := RunStats{}
	if limit <= 0 {
		limit = 5
	}
	sources, err := c.Store.DueWatchSources(ctx, c.now(), limit)
	if err != nil {
		return stats, err
	}
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		started := time.Now()
		result, checkErr := c.CheckAndSave(ctx, source)
		stats.Fetched++
		if checkErr != nil {
			stats.Errors++
		} else {
			switch result.Status {
			case StatusChanged:
				stats.Changed++
			case StatusUnchanged:
				stats.Unchanged++
			case StatusNeedsParser:
				stats.NeedsParser++
			case StatusNeedsAttention:
				stats.NeedsAttention++
			default:
				stats.Errors++
			}
		}
		attrs := []any{
			"source_id", source.ID,
			"program_key", source.ProgramKey,
			"url", source.URL,
			"status", result.Status,
			"duration_ms", time.Since(started).Milliseconds(),
		}
		if result.DraftID != "" {
			attrs = append(attrs, "draft_id", result.DraftID)
		}
		if result.Error != "" {
			attrs = append(attrs, "error", result.Error)
		}
		if checkErr != nil {
			attrs = append(attrs, "save_error", checkErr.Error())
			c.logger().Error("program_watch_check_failed", attrs...)
			continue
		}
		if result.Status == StatusError || result.Status == StatusNeedsParser || result.Status == StatusNeedsAttention {
			c.logger().Warn("program_watch_checked", attrs...)
		} else {
			c.logger().Info("program_watch_checked", attrs...)
		}
	}
	return stats, nil
}

// RunOnceAndLog — удобная обёртка для cmd/program-watcher -once.
func (c *Checker) RunOnceAndLog(ctx context.Context, limit int) (RunStats, error) {
	stats, err := c.RunOnce(ctx, limit)
	if err != nil {
		return stats, err
	}
	c.logger().Info("program_watch_cycle_finished",
		"fetched", stats.Fetched,
		"changed", stats.Changed,
		"unchanged", stats.Unchanged,
		"needs_parser", stats.NeedsParser,
		"needs_attention", stats.NeedsAttention,
		"errors", stats.Errors)
	return stats, nil
}

func pauseUntil(now time.Time, intervalHours, failures int) time.Time {
	if failures < 1 {
		return now
	}
	base := time.Duration(intervalHours) * time.Hour
	if base <= 0 {
		base = 24 * time.Hour
	}
	delay := base
	for i := 1; i < failures; i++ {
		if delay >= maxBackoff/2 {
			delay = maxBackoff
			break
		}
		delay *= 2
	}
	if delay > maxBackoff {
		delay = maxBackoff
	}
	return now.Add(delay - base)
}

func errorResult(message string) (CheckResult, CheckUpdate) {
	return CheckResult{Status: StatusError, Error: message}, CheckUpdate{}
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("ответ больше %d МБ", limit>>20)
	}
	return data, nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func isHTML(contentType string, body []byte) bool {
	value := strings.ToLower(contentType)
	if strings.Contains(value, "text/html") || strings.Contains(value, "application/xhtml+xml") {
		return true
	}
	if value != "" {
		return false
	}
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) > 512 {
		trimmed = trimmed[:512]
	}
	trimmed = strings.ToLower(trimmed)
	return strings.HasPrefix(trimmed, "<!doctype html") || strings.HasPrefix(trimmed, "<html")
}

func firstDocumentLink(body []byte, base *url.URL) (string, bool) {
	for _, match := range htmlHrefPattern.FindAllSubmatch(body, -1) {
		raw := ""
		for i := 1; i < len(match); i++ {
			if len(match[i]) > 0 {
				raw = string(match[i])
				break
			}
		}
		raw = strings.TrimSpace(strings.ReplaceAll(raw, "&amp;", "&"))
		if raw == "" {
			continue
		}
		reference, err := url.Parse(raw)
		if err != nil {
			continue
		}
		resolved := reference
		if base != nil {
			resolved = base.ResolveReference(reference)
		}
		if resolved.Scheme != "http" && resolved.Scheme != "https" {
			continue
		}
		if watchedDocumentExtensions[strings.ToLower(filepath.Ext(resolved.Path))] {
			return resolved.String(), true
		}
	}
	return "", false
}

func resolveDocumentFormat(explicit, name, contentType string) string {
	if value := strings.ToLower(strings.TrimSpace(explicit)); value != "" {
		return value
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	switch ext {
	case "xlsx", "csv", "json", "txt", "md", "pdf", "docx":
		return ext
	}
	value := strings.ToLower(contentType)
	switch {
	case strings.Contains(value, "application/pdf"):
		return "pdf"
	case strings.Contains(value, "wordprocessingml"):
		return "docx"
	}
	return ext
}

func isParsedProgramFormat(format string) bool {
	switch format {
	case "csv", "xlsx", "json", "txt", "md":
		return true
	default:
		return false
	}
}

func parserFileName(name, format string) string {
	base := strings.TrimSpace(filepath.Base(name))
	if base == "" || base == "." {
		base = "document"
	}
	ext := strings.ToLower(filepath.Ext(base))
	if strings.TrimPrefix(ext, ".") == format {
		return base
	}
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return base + "." + format
}

func fileNameFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "document"
	}
	name := filepath.Base(parsed.Path)
	if name == "" || name == "." || name == "/" || name == string(filepath.Separator) {
		return "document"
	}
	return name
}

func countKSA(program ingestion.EducationProgram) int {
	count := 0
	for _, competency := range program.Competencies {
		count += len(competency.KSA)
	}
	return count
}

func compactError(message string) string {
	runes := []rune(strings.TrimSpace(message))
	if len(runes) <= maxStoredErrorRunes {
		return string(runes)
	}
	return string(runes[:maxStoredErrorRunes]) + "…"
}
