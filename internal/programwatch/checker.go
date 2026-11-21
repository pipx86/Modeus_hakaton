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

	"activity-descriptor/internal/fgos"
	"activity-descriptor/internal/ingestion"
)

const (
	defaultMaxDocumentBytes int64 = 20 << 20
	defaultHTTPTimeout            = 60 * time.Second
	maxStoredErrorRunes           = 1000
	maxBackoff                    = 30 * 24 * time.Hour
	defaultUserAgent              = "program-watcher/1.0"
	// releaseTimeout ограничивает best-effort Release, чтобы недоступная БД не
	// задерживала обработку следующего источника.
	releaseTimeout = 5 * time.Second
	// acceptDocuments — что воркер готов принять по ссылке со страницы.
	acceptDocuments = "text/html,application/xhtml+xml,application/json,text/csv," +
		"application/pdf,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet," +
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document,text/plain,*/*"
)

var htmlHrefPattern = regexp.MustCompile(`(?is)href\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)

var watchedDocumentExtensions = map[string]bool{
	".xlsx": true,
	".csv":  true,
	".json": true,
	".txt":  true,
	".md":   true,
	".pdf":  true,
	".docx": true,
}

// PDFExtractor — необязательный LLM-адаптер ФГОС для PDF и DOCX. Контракт живёт
// в internal/fgos, здесь он вынесен отдельным именем, чтобы тесты подставляли
// фейк без сети. nil означает прежнее поведение: статус needs_parser.
type PDFExtractor = fgos.DocumentExtractor

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
	// PDFExtractor включает разбор PDF/DOCX через LLM. nil — needs_parser.
	PDFExtractor PDFExtractor
	// Owner — идентификатор владельца lease-блокировки для RunOnce. Если пуст,
	// используется hostname-pid.
	Owner string
	// Lease — срок lease-блокировки для RunOnce. Если не задан, используется
	// DefaultLease.
	Lease time.Duration
}

// NewChecker создаёт чекер с боевыми настройками по умолчанию.
func NewChecker(store Store, drafts DraftCreator) *Checker {
	return &Checker{Store: store, Drafts: drafts}
}

// CheckAndSave синхронно проверяет один источник, применяет экспоненциальную
// паузу при ошибке и сохраняет статус/валидаторы/черновик. owner — владелец
// уже взятой lease-блокировки: успешный SaveWatchCheck снимает её, а ошибка
// сохранения освобождает блокировку через ReleaseWatchSource.
func (c *Checker) CheckAndSave(ctx context.Context, source Source, owner string) (CheckResult, error) {
	now := c.now()
	result, update := c.check(ctx, source)
	update.CheckedAt = now
	update.Owner = owner
	// Статус и текст ошибки живут в результате проверки; сохраняем их вместе
	// с валидаторами, иначе в реестре остаётся пустой last_status.
	update.Status = result.Status
	if result.Error != "" {
		update.Error = result.Error
	}
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
		// Результат не сохранён — lease нужно вернуть сразу, не дожидаясь его
		// истечения, иначе источник простаивает до locked_until.
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
		defer cancel()
		if releaseErr := c.Store.ReleaseWatchSource(releaseCtx, source.ID, owner); releaseErr != nil {
			c.logger().Warn("program_watch_release_failed",
				"source_id", source.ID, "owner", owner, "error", releaseErr)
		}
		return result, err
	}
	return result, nil
}

// check — одна проверка источника. Порядок важен: сначала сравнивается
// отпечаток привязанного документа, и только потом решается, изменился ли
// источник. Иначе обновление xlsx/pdf по неизменившейся HTML-странице
// пропускалось бы.
func (c *Checker) check(ctx context.Context, source Source) (CheckResult, CheckUpdate) {
	page, err := c.fetchPage(ctx, source)
	if err != nil {
		return errorResult(err.Error())
	}
	if page.notModified {
		// Страница не менялась, но привязанный документ мог обновиться:
		// проверяем сохранённый document_url по его собственным валидаторам.
		return c.checkAttachedDocument(ctx, source)
	}

	pageHash := sha256Hex(page.body)
	pageUnchanged := source.LastHash != "" && pageHash == source.LastHash

	if isHTML(page.contentType, page.body) {
		documentURL, ok := firstDocumentLink(page.body, page.url)
		if !ok {
			return CheckResult{Status: StatusNeedsAttention, Error: "на странице нет ссылки на документ"}, CheckUpdate{}
		}
		return c.checkRemoteDocument(ctx, source, page, documentURL)
	}

	// Прямой URL: страница и документ — одно и то же тело, поэтому отдельный
	// условный GET документу не нужен.
	documentURL := source.URL
	if pageUnchanged {
		return unchangedWithFingerprint(page, documentURL, pageHash)
	}
	if sameDocument(source, documentURL) && source.DocumentHash != "" && source.DocumentHash == pageHash {
		return unchangedWithFingerprint(page, documentURL, pageHash)
	}

	result, update := c.processDocument(ctx, source, documentURL, fileNameFromURL(documentURL), page.contentType, page.body)
	if result.Status != StatusChanged {
		return result, update
	}
	setPageValidators(&update, page, pageHash)
	setDocumentValidators(&update, documentURL, page.etag, page.lastModified, pageHash)
	return result, update
}

// checkRemoteDocument скачивает документ по ссылке со страницы. Условный GET
// отправляется только когда ссылка та же, что сохранённый document_url:
// валидаторы относятся к конкретному URL и не должны применяться к новому.
func (c *Checker) checkRemoteDocument(ctx context.Context, source Source, page pageResponse, documentURL string) (CheckResult, CheckUpdate) {
	knownURL := ""
	etag, lastModified := "", ""
	if sameDocument(source, documentURL) {
		knownURL = source.DocumentURL
		etag, lastModified = source.DocumentETag, source.DocumentLastModified
	}
	document, err := c.fetchDocumentConditional(ctx, documentURL, knownURL, etag, lastModified)
	if err != nil {
		return errorResult(err.Error())
	}
	update := CheckUpdate{}
	if document.notModified {
		// 304 документа означает «не менялся», даже если страница изменилась.
		setPageValidators(&update, page, sha256Hex(page.body))
		return CheckResult{Status: StatusUnchanged}, update
	}
	documentHash := sha256Hex(document.body)
	if sameDocument(source, documentURL) && source.DocumentHash != "" && source.DocumentHash == documentHash {
		setPageValidators(&update, page, sha256Hex(page.body))
		setDocumentValidators(&update, documentURL, document.etag, document.lastModified, documentHash)
		return CheckResult{Status: StatusUnchanged}, update
	}

	result, docUpdate := c.processDocument(ctx, source, documentURL, fileNameFromURL(documentURL), document.contentType, document.body)
	if result.Status != StatusChanged {
		return result, docUpdate
	}
	setPageValidators(&docUpdate, page, sha256Hex(page.body))
	setDocumentValidators(&docUpdate, documentURL, document.etag, document.lastModified, documentHash)
	return result, docUpdate
}

// checkAttachedDocument проверяет документ, когда страница ответила 304. Без
// сохранённого document_url проверить нечего, поэтому источник остаётся
// unchanged.
func (c *Checker) checkAttachedDocument(ctx context.Context, source Source) (CheckResult, CheckUpdate) {
	if strings.TrimSpace(source.DocumentURL) == "" {
		return CheckResult{Status: StatusUnchanged}, CheckUpdate{}
	}
	document, err := c.fetchDocumentConditional(ctx, source.DocumentURL, source.DocumentURL, source.DocumentETag, source.DocumentLastModified)
	if err != nil {
		return errorResult(err.Error())
	}
	update := CheckUpdate{}
	if document.notModified {
		return CheckResult{Status: StatusUnchanged}, update
	}
	documentHash := sha256Hex(document.body)
	if source.DocumentHash != "" && source.DocumentHash == documentHash {
		setDocumentValidators(&update, source.DocumentURL, document.etag, document.lastModified, documentHash)
		return CheckResult{Status: StatusUnchanged}, update
	}
	result, docUpdate := c.processDocument(ctx, source, source.DocumentURL, fileNameFromURL(source.DocumentURL), document.contentType, document.body)
	if result.Status != StatusChanged {
		return result, docUpdate
	}
	setDocumentValidators(&docUpdate, source.DocumentURL, document.etag, document.lastModified, documentHash)
	return result, docUpdate
}

// processDocument выбирает способ разбора по формату: встроенные парсеры для
// csv/xlsx/json/txt/md, LLM-адаптер для pdf/docx. Валидаторы здесь не
// выставляются — это делает вызывающий, зная URL и заголовки ответа.
func (c *Checker) processDocument(ctx context.Context, source Source, documentURL, documentName, contentType string, documentBody []byte) (CheckResult, CheckUpdate) {
	format := resolveDocumentFormat(source.DocFormat, documentName, contentType)
	switch format {
	case "pdf", "docx":
		return c.processPDF(ctx, source, documentURL, documentName, format, documentBody)
	case "html":
		return errorResult("источник вернул HTML вместо документа программы")
	}
	if !isParsedProgramFormat(format) {
		return CheckResult{
			Status: StatusNeedsParser,
			Error:  fmt.Sprintf("формат %q не поддерживается парсером программ", format),
		}, CheckUpdate{DocumentURL: stringPointer(documentURL)}
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
	return c.createDraft(ctx, source.ProgramKey, documentName, ingestion.ProgramSourceFormat(parserName), program, warnings)
}

// processPDF разбирает PDF/DOCX через необязательный LLM-адаптер ФГОС. Без
// адаптера формат остаётся в статусе needs_parser, но document_url уже
// сохраняется: включённый позже адаптер сразу увидит, какой документ проверять.
func (c *Checker) processPDF(ctx context.Context, source Source, documentURL, documentName, format string, documentBody []byte) (CheckResult, CheckUpdate) {
	if c.PDFExtractor == nil {
		return CheckResult{Status: StatusNeedsParser, Error: "PDF и DOCX требуют LLM-адаптера"},
			CheckUpdate{DocumentURL: stringPointer(documentURL)}
	}
	pages, err := c.PDFExtractor.ExtractPages(documentName, documentBody)
	if err != nil {
		return errorResult(fmt.Sprintf("не удалось прочитать %s: %s", strings.ToUpper(format), compactError(err.Error())))
	}
	program, warnings, err := c.PDFExtractor.Extract(ctx, pages)
	if err != nil {
		return errorResult(fmt.Sprintf("не удалось извлечь программу из %s: %s", strings.ToUpper(format), compactError(err.Error())))
	}
	if warning := fgos.ApproximatePageNumbersWarningFor(format); warning != "" {
		warnings = append([]string{warning}, warnings...)
	}
	if strings.TrimSpace(program.Title) == "" {
		program.Title = programTitleFromName(documentName)
	}
	if strings.TrimSpace(program.Code) == "" {
		program.Code = source.ProgramKey
	}
	return c.createDraft(ctx, source.ProgramKey, documentName, format, program, warnings)
}

// createDraft создаёт черновик и возвращает changed с его идентификатором.
func (c *Checker) createDraft(ctx context.Context, programKey, documentName, sourceFormat string, program ingestion.EducationProgram, warnings []string) (CheckResult, CheckUpdate) {
	if c.Drafts == nil {
		return errorResult("создатель черновиков не настроен")
	}
	if warnings == nil {
		warnings = []string{}
	}
	draftID, draftErr := c.Drafts.CreateProgramWatchDraft(ctx, DraftInput{
		ProgramKey:   programKey,
		SourceName:   documentName,
		SourceFormat: sourceFormat,
		Program:      program,
		Warnings:     warnings,
		Competencies: len(program.Competencies),
		KSA:          countKSA(program),
	})
	if draftErr != nil {
		return errorResult("не удалось создать черновик: " + compactError(draftErr.Error()))
	}
	return CheckResult{Status: StatusChanged, DraftID: draftID}, CheckUpdate{DraftID: draftID}
}

// pageResponse — успешный ответ страницы источника.
type pageResponse struct {
	body         []byte
	url          *url.URL
	etag         string
	lastModified string
	contentType  string
	notModified  bool
}

// fetchPage скачивает страницу источника с сохранёнными валидаторами.
func (c *Checker) fetchPage(ctx context.Context, source Source) (pageResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return pageResponse{}, fmt.Errorf("некорректный url источника: %v", err)
	}
	request.Header.Set("User-Agent", c.userAgent())
	request.Header.Set("Accept", acceptDocuments)
	if source.ETag != "" {
		request.Header.Set("If-None-Match", source.ETag)
	}
	if source.LastModified != "" {
		request.Header.Set("If-Modified-Since", source.LastModified)
	}
	response, err := c.client().Do(request)
	if err != nil {
		return pageResponse{}, fmt.Errorf("источник недоступен: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	switch response.StatusCode {
	case http.StatusNotModified:
		return pageResponse{notModified: true}, nil
	case http.StatusOK:
	default:
		return pageResponse{}, fmt.Errorf("источник ответил HTTP %d", response.StatusCode)
	}
	body, err := readLimited(response.Body, c.maxDocumentBytes())
	if err != nil {
		return pageResponse{}, fmt.Errorf("не удалось прочитать ответ: %v", err)
	}
	return pageResponse{
		body:         body,
		url:          response.Request.URL,
		etag:         response.Header.Get("ETag"),
		lastModified: response.Header.Get("Last-Modified"),
		contentType:  response.Header.Get("Content-Type"),
	}, nil
}

// documentResponse — ответ условного GET документа программы.
type documentResponse struct {
	body         []byte
	etag         string
	lastModified string
	contentType  string
	notModified  bool
}

// fetchDocumentConditional скачивает документ, отправляя If-None-Match и
// If-Modified-Since, только если известен тот же URL, что и в прошлой
// проверке. Ответ 304 возвращается как notModified без тела.
func (c *Checker) fetchDocumentConditional(ctx context.Context, rawURL, knownURL, etag, lastModified string) (documentResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return documentResponse{}, fmt.Errorf("некорректная ссылка на документ: %w", err)
	}
	request.Header.Set("User-Agent", c.userAgent())
	request.Header.Set("Accept", acceptDocuments)
	if knownURL != "" && rawURL == knownURL {
		if etag != "" {
			request.Header.Set("If-None-Match", etag)
		}
		if lastModified != "" {
			request.Header.Set("If-Modified-Since", lastModified)
		}
	}
	response, err := c.client().Do(request)
	if err != nil {
		return documentResponse{}, fmt.Errorf("не удалось скачать документ: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusNotModified:
		return documentResponse{notModified: true}, nil
	case http.StatusOK:
	default:
		return documentResponse{}, fmt.Errorf("документ ответил HTTP %d", response.StatusCode)
	}
	body, err := readLimited(response.Body, c.maxDocumentBytes())
	if err != nil {
		return documentResponse{}, fmt.Errorf("не удалось прочитать документ: %v", err)
	}
	return documentResponse{
		body:         body,
		etag:         response.Header.Get("ETag"),
		lastModified: response.Header.Get("Last-Modified"),
		contentType:  response.Header.Get("Content-Type"),
	}, nil
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

// owner — владелец lease-блокировки текущего цикла.
func (c *Checker) owner() string {
	if trimmed := strings.TrimSpace(c.Owner); trimmed != "" {
		return trimmed
	}
	return DefaultOwner()
}

// lease — срок lease-блокировки текущего цикла.
func (c *Checker) lease() time.Duration {
	if c.Lease > 0 {
		return c.Lease
	}
	return DefaultLease
}

func (c *Checker) maxDocumentBytes() int64 {
	if c.MaxDocumentBytes > 0 {
		return c.MaxDocumentBytes
	}
	return defaultMaxDocumentBytes
}

func (c *Checker) userAgent() string {
	if trimmed := strings.TrimSpace(c.UserAgent); trimmed != "" {
		return trimmed
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

// RunOnce берёт lease-блокировку на не больше limit просроченных источников и
// проверяет их последовательно. Claim атомарный, поэтому параллельный воркер
// или ручная проверка не получит те же строки.
func (c *Checker) RunOnce(ctx context.Context, limit int) (RunStats, error) {
	stats := RunStats{}
	if limit <= 0 {
		limit = 5
	}
	owner := c.owner()
	lease := c.lease()
	sources, err := c.Store.ClaimDueWatchSources(ctx, c.now(), limit, lease, owner)
	if err != nil {
		return stats, err
	}
	for index, source := range sources {
		if ctxErr := ctx.Err(); ctxErr != nil {
			c.releaseClaims(ctx, sources[index:], owner)
			return stats, ctxErr
		}
		started := time.Now()
		result, checkErr := c.CheckAndSave(ctx, source, owner)
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
			"owner", owner,
			"lease", lease.String(),
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

// releaseClaims возвращает lease-блокировки источников, которые цикл не успел
// обработать, чтобы они не простаивали до истечения lease.
func (c *Checker) releaseClaims(ctx context.Context, sources []Source, owner string) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	for _, source := range sources {
		if err := c.Store.ReleaseWatchSource(releaseCtx, source.ID, owner); err != nil {
			c.logger().Warn("program_watch_release_failed",
				"source_id", source.ID, "owner", owner, "error", err)
		}
	}
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
		"errors", stats.Errors,
		"owner", c.owner(),
		"lease", c.lease().String())
	return stats, nil
}

// unchangedWithFingerprint возвращает unchanged и обновляет оба отпечатка: и
// страницы, и документа. Для прямого URL они совпадают, но document_url нужен,
// чтобы следующий запуск знал, что за документ проверяется.
func unchangedWithFingerprint(page pageResponse, documentURL, documentHash string) (CheckResult, CheckUpdate) {
	update := CheckUpdate{}
	setPageValidators(&update, page, sha256Hex(page.body))
	setDocumentValidators(&update, documentURL, page.etag, page.lastModified, documentHash)
	return CheckResult{Status: StatusUnchanged}, update
}

// setPageValidators сохраняет отпечаток страницы источника.
func setPageValidators(update *CheckUpdate, page pageResponse, pageHash string) {
	if update == nil {
		return
	}
	etag := page.etag
	lastModified := page.lastModified
	hash := pageHash
	update.ETag = &etag
	update.LastModified = &lastModified
	update.LastHash = &hash
}

// setDocumentValidators сохраняет отпечаток привязанного документа.
func setDocumentValidators(update *CheckUpdate, documentURL, etag, lastModified, documentHash string) {
	if update == nil {
		return
	}
	update.DocumentURL = stringPointer(documentURL)
	update.DocumentETag = stringPointer(etag)
	update.DocumentLastModified = stringPointer(lastModified)
	update.DocumentHash = stringPointer(documentHash)
}

// sameDocument сообщает, что проверяемая ссылка совпадает с сохранённой:
// только тогда можно сравнивать хэши и отправлять условные заголовки.
func sameDocument(source Source, documentURL string) bool {
	return source.DocumentURL != "" && source.DocumentURL == documentURL
}

func stringPointer(value string) *string {
	return &value
}

// programTitleFromName строит название программы по имени файла, когда модель
// не вернула title: черновик должен быть читаемым до публикации.
func programTitleFromName(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	base = strings.TrimSpace(strings.TrimSuffix(base, filepath.Ext(base)))
	if base == "" || base == "." {
		return "Образовательная программа"
	}
	return base
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
