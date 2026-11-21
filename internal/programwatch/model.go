// Package programwatch хранит реестр источников образовательных программ и
// проверяет их по расписанию: HTTP-условия (ETag/Last-Modified), хэш тела,
// разбор документа и создание черновика для администратора.
package programwatch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"activity-descriptor/internal/ingestion"
)

// Статусы проверки. Они же значения program_watch_sources.last_status и поля
// status в ответе POST /admin/program-sources/{id}/check.
const (
	StatusUnchanged      = "unchanged"
	StatusChanged        = "changed"
	StatusNeedsParser    = "needs_parser"
	StatusNeedsAttention = "needs_attention"
	StatusError          = "error"
)

var (
	// ErrNotFound — источник не найден (404).
	ErrNotFound = errors.New("PROGRAM_WATCH_SOURCE_NOT_FOUND")
	// ErrDuplicate — пара program_key+url уже есть в реестре (409).
	ErrDuplicate = errors.New("PROGRAM_WATCH_SOURCE_DUPLICATE")
	// ErrWatchSourceBusy — источник уже проверяется другим воркером или
	// администратором: lease-блокировка не истекла (409).
	ErrWatchSourceBusy = errors.New("PROGRAM_SOURCE_BUSY")
	// ErrUnavailable — хранилище не поддерживает реестр (501).
	ErrUnavailable = errors.New("PROGRAM_WATCH_SOURCE_UNAVAILABLE")
)

// Source — строка реестра program_watch_sources.
type Source struct {
	ID            string `json:"id"`
	ProgramKey    string `json:"program_key"`
	URL           string `json:"url"`
	DocFormat     string `json:"doc_format,omitempty"`
	IntervalHours int    `json:"interval_hours"`
	Enabled       bool   `json:"enabled"`
	// ETag/LastModified/LastHash — отпечаток страницы источника.
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
	LastHash     string `json:"last_hash,omitempty"`
	// DocumentURL/DocumentHash/DocumentETag/DocumentLastModified — отпечаток
	// привязанного документа. Страница может не меняться, пока документ по
	// ссылке обновляется; проверка сравнивает именно document_hash.
	DocumentURL          string     `json:"document_url,omitempty"`
	DocumentHash         string     `json:"document_hash,omitempty"`
	DocumentETag         string     `json:"document_etag,omitempty"`
	DocumentLastModified string     `json:"document_last_modified,omitempty"`
	LastCheckedAt        *time.Time `json:"last_checked_at,omitempty"`
	LastStatus           string     `json:"last_status,omitempty"`
	LastError            string     `json:"last_error,omitempty"`
	DraftID              string     `json:"draft_id,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	FailureCount         int        `json:"-"`
}

// CreateInput — тело POST /admin/program-sources после нормализации.
type CreateInput struct {
	ProgramKey    string
	URL           string
	DocFormat     string
	IntervalHours int
}

// SourcePatch — частичное обновление; nil означает «не менять».
type SourcePatch struct {
	Enabled       *bool
	IntervalHours *int
}

// CheckUpdate — то, что проверка разрешает записать в строку реестра. Указатели
// на валидаторы позволяют отличить «не трогать» (nil) от «очистить» (пустая
// строка): при 304 и сетевой ошибке старые ETag/Last-Modified/хэш сохраняются.
// Owner — владелец lease-блокировки: репозиторий снимает блокировку только
// если locked_by совпадает с ним.
type CheckUpdate struct {
	CheckedAt    time.Time
	Status       string
	Error        string
	DraftID      string
	ETag         *string
	LastModified *string
	LastHash     *string
	// DocumentURL/DocumentHash/DocumentETag/DocumentLastModified — отпечаток
	// документа. nil означает «оставить прежнее значение», поэтому 304
	// документа и ошибка разбора не затирают сохранённый отпечаток.
	DocumentURL          *string
	DocumentHash         *string
	DocumentETag         *string
	DocumentLastModified *string
	FailureCount         int
	Owner                string
}

// CheckResult — публичный ответ проверки.
type CheckResult struct {
	Status  string `json:"status"`
	DraftID string `json:"draft_id,omitempty"`
	Error   string `json:"error,omitempty"`
}

// DraftInput — данные, необходимые создателю черновика. Пакет API преобразует
// их в ProgramImportDraft, чтобы programwatch не зависел от api.
type DraftInput struct {
	ProgramKey   string
	SourceName   string
	SourceFormat string
	Program      ingestion.EducationProgram
	Warnings     []string
	Competencies int
	KSA          int
}

// DraftCreator создаёт черновик и возвращает его ID.
type DraftCreator interface {
	CreateProgramWatchDraft(ctx context.Context, input DraftInput) (string, error)
}

// Store — реестр источников, нужный API и воркеру.
//
// Проверяющая сторона сначала атомарно берёт lease-блокировку через
// ClaimDueWatchSources (воркер) или ClaimWatchSource (ручная проверка), затем
// сохраняет результат через SaveWatchCheck, который снимает блокировку, либо
// освобождает её через ReleaseWatchSource при ошибке. Пока блокировка жива,
// второй претендент получает ErrWatchSourceBusy вместо повторной проверки.
type Store interface {
	CreateWatchSource(ctx context.Context, input CreateInput) (Source, error)
	ListWatchSources(ctx context.Context) ([]Source, error)
	GetWatchSource(ctx context.Context, id string) (Source, error)
	UpdateWatchSource(ctx context.Context, id string, patch SourcePatch) (Source, error)
	DeleteWatchSource(ctx context.Context, id string) error
	// ClaimDueWatchSources выбирает до limit просроченных источников и в одном
	// UPDATE ставит на них lease-блокировку с владельцем owner.
	ClaimDueWatchSources(ctx context.Context, now time.Time, limit int, lease time.Duration, owner string) ([]Source, error)
	// ClaimWatchSource ставит lease-блокировку на один источник. Занятый
	// источник возвращает ErrWatchSourceBusy, отсутствующий — ErrNotFound.
	ClaimWatchSource(ctx context.Context, id string, lease time.Duration, owner string) (Source, error)
	// ReleaseWatchSource снимает блокировку, если её владелец совпадает с owner.
	ReleaseWatchSource(ctx context.Context, id, owner string) error
	SaveWatchCheck(ctx context.Context, id string, update CheckUpdate) error
}

var docFormats = map[string]bool{
	"csv":   true,
	"xlsx":  true,
	"json":  true,
	"txt":   true,
	"md":    true,
	"pdf":   true,
	"docx":  true,
	"html":  true,
	"other": true,
}

// NormalizeCreateInput проверяет и нормализует входные данные реестра. Ключ
// здесь уже должен быть каноническим — это проверяет вызывающий API.
func NormalizeCreateInput(input CreateInput) (CreateInput, error) {
	input.ProgramKey = strings.TrimSpace(input.ProgramKey)
	input.URL = strings.TrimSpace(input.URL)
	input.DocFormat = strings.ToLower(strings.TrimSpace(input.DocFormat))
	if input.ProgramKey == "" {
		return input, errors.New("program_key обязателен")
	}
	normalizedURL, err := NormalizeSourceURL(input.URL)
	if err != nil {
		return input, err
	}
	input.URL = normalizedURL
	if input.DocFormat != "" && !docFormats[input.DocFormat] {
		return input, fmt.Errorf("doc_format %q не поддерживается", input.DocFormat)
	}
	if input.IntervalHours == 0 {
		input.IntervalHours = 24
	}
	if input.IntervalHours < 1 || input.IntervalHours > 720 {
		return input, errors.New("interval_hours должен быть от 1 до 720")
	}
	return input, nil
}

// NormalizeSourceURL разрешает только абсолютные http/https URL.
func NormalizeSourceURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("url обязателен")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("url %q не разбирается: %w", value, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("url %q должен начинаться с http:// или https://", value)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("url %q должен содержать хост", value)
	}
	return parsed.String(), nil
}

// ValidatePatch проверяет частичное обновление.
func ValidatePatch(patch SourcePatch) error {
	if patch.Enabled == nil && patch.IntervalHours == nil {
		return errors.New("нужно хотя бы одно поле: enabled или interval_hours")
	}
	if patch.IntervalHours != nil && (*patch.IntervalHours < 1 || *patch.IntervalHours > 720) {
		return errors.New("interval_hours должен быть от 1 до 720")
	}
	return nil
}

// Due сообщает, пора ли проверять источник. Источник без last_checked_at
// считается новым и проверяется первым.
func (s Source) Due(now time.Time) bool {
	if !s.Enabled {
		return false
	}
	if s.LastCheckedAt == nil {
		return true
	}
	interval := time.Duration(s.IntervalHours) * time.Hour
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	return !s.LastCheckedAt.Add(interval).After(now)
}

// SelectDue — чистая функция выбора источников на цикл. SQL-репозиторий
// повторяет те же условия; функция нужна, чтобы порядок и лимит проверялись
// без PostgreSQL.
func SelectDue(sources []Source, now time.Time, limit int) []Source {
	due := make([]Source, 0, len(sources))
	for _, source := range sources {
		if source.Due(now) {
			due = append(due, source)
		}
	}
	sort.SliceStable(due, func(i, j int) bool {
		switch {
		case due[i].LastCheckedAt == nil && due[j].LastCheckedAt == nil:
			return due[i].ID < due[j].ID
		case due[i].LastCheckedAt == nil:
			return true
		case due[j].LastCheckedAt == nil:
			return false
		default:
			return due[i].LastCheckedAt.Before(*due[j].LastCheckedAt)
		}
	})
	if limit > 0 && len(due) > limit {
		due = due[:limit]
	}
	return due
}
