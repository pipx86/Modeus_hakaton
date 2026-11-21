// Package fgos извлекает компетенции и KSA из документов ФГОС (PDF/DOCX)
// через существующий LLM-клиент. Адаптер ничего не пишет в БД: он возвращает
// ingestion.EducationProgram и предупреждения, а черновик создаёт вызывающий
// код (programwatch.Checker или API импорта программ).
//
// Пайплайн:
//
//  1. ExtractPages режет PDF/DOCX на страницы с текстом;
//  2. Extract режет страницы на чанки, не разрывая страницу, и просит модель
//     вернуть JSON с компетенциями и дословными цитатами;
//  3. каждый пункт валидируется (код, тип, цитата на указанной странице);
//     невалидные пункты отбрасываются с предупреждением;
//  4. результаты чанков дедуплицируются по коду компетенции с объединением KSA.
package fgos

import (
	"context"
	"errors"
	"strings"
	"time"

	"activity-descriptor/internal/ingestion"
	"activity-descriptor/internal/llm"
)

// PromptVersion — версия промпта, встроенная в бинарь (prompts/fgos/<version>).
const PromptVersion = "v1"

// Значения по умолчанию для Extractor.
const (
	// DefaultMaxPages ограничивает документ, который адаптер берётся разбирать:
	// типовой ФГОС укладывается в 60–80 страниц, а скан на 300 страниц дороже
	// и чаще всего требует OCR.
	DefaultMaxPages = 80
	// DefaultMaxCharsPerChunk — целевой размер чанка. Один запрос на ~20k
	// символов держит ответ модели в разумном размере и не упирается в лимит
	// контекста.
	DefaultMaxCharsPerChunk = 20000
	// DefaultTimeout ограничивает весь разбор одного документа.
	DefaultTimeout = 5 * time.Minute
)

// Ошибки адаптера. Вызывающий код матчит их через errors.Is, чтобы выбрать
// HTTP-код (422 для документа, 503 для недоступной модели).
var (
	// ErrNoTextLayer — в документе нет текстового слоя (скан): нужен OCR.
	ErrNoTextLayer = errors.New("fgos: нет текстового слоя, нужен OCR")
	// ErrUnsupportedFormat — формат файла не PDF и не DOCX.
	ErrUnsupportedFormat = errors.New("fgos: поддерживаются только PDF и DOCX")
	// ErrInvalidDocument — файл повреждён, защищён паролем или не читается.
	ErrInvalidDocument = errors.New("fgos: документ не читается")
	// ErrNoCompetencies — модель не вернула ни одного валидного пункта.
	ErrNoCompetencies = errors.New("fgos: не удалось извлечь компетенции")
	// ErrNoClient — адаптер создан без LLM-клиента.
	ErrNoClient = errors.New("fgos: LLM-клиент не настроен")
	// ErrTimeout — разбор не уложился в Extractor.Timeout.
	ErrTimeout = errors.New("fgos: превышен таймаут извлечения")
)

// Page — страница документа с текстом. Number — номер страницы в документе
// (с 1). Для DOCX нумерация приблизительная: формат не хранит разбиение.
type Page struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
}

// DocumentExtractor — контракт адаптера для programwatch.Checker и API
// импорта программ. В тестах подменяется фейком без сети.
type DocumentExtractor interface {
	ExtractPages(name string, data []byte) ([]Page, error)
	Extract(ctx context.Context, pages []Page) (ingestion.EducationProgram, []string, error)
}

// Extractor — LLM-адаптер ФГОС поверх llm.ChatClient. Поля можно менять
// напрямую; нулевые значения заменяются на Default*.
type Extractor struct {
	// Client — существующий LLM-клиент проекта (llm.ChatClient). Обязателен.
	Client llm.ChatClient
	// MaxPages — максимум страниц в документе; превышение даёт ошибку.
	MaxPages int
	// MaxCharsPerChunk — целевой размер чанка в символах.
	MaxCharsPerChunk int
	// Timeout — бюджет времени на весь разбор документа.
	Timeout time.Duration
}

// NewExtractor создаёт адаптер с боевыми настройками по умолчанию.
func NewExtractor(client llm.ChatClient) *Extractor {
	return &Extractor{
		Client:           client,
		MaxPages:         DefaultMaxPages,
		MaxCharsPerChunk: DefaultMaxCharsPerChunk,
		Timeout:          DefaultTimeout,
	}
}

// ExtractPages возвращает страницы документа по его байтам. name нужен только
// для определения формата по расширению.
func (e *Extractor) ExtractPages(name string, data []byte) ([]Page, error) {
	return ExtractPages(name, data)
}

// ExtractPagesFile читает файл с диска и возвращает страницы документа.
func (e *Extractor) ExtractPagesFile(path string) ([]Page, error) {
	return ExtractPagesFile(path)
}

func (e *Extractor) maxPages() int {
	if e != nil && e.MaxPages > 0 {
		return e.MaxPages
	}
	return DefaultMaxPages
}

func (e *Extractor) maxCharsPerChunk() int {
	if e != nil && e.MaxCharsPerChunk > 0 {
		return e.MaxCharsPerChunk
	}
	return DefaultMaxCharsPerChunk
}

func (e *Extractor) timeout() time.Duration {
	if e != nil && e.Timeout > 0 {
		return e.Timeout
	}
	return DefaultTimeout
}

// ApproximatePageNumbersWarning добавляется к предупреждениям черновика для
// DOCX: библиотека не даёт точных номеров страниц, цитаты проверяются по
// порядку страниц, восстановленному по разрывам.
const ApproximatePageNumbersWarning = "номера страниц приблизительные: DOCX не хранит постраничное разбиение, цитаты проверяются по порядку страниц документа"

// ApproximatePageNumbersWarningFor возвращает предупреждение о приблизительных
// номерах страниц для формата или пустую строку, если формат даёт точные номера.
func ApproximatePageNumbersWarningFor(format string) string {
	if strings.EqualFold(strings.TrimSpace(format), "docx") {
		return ApproximatePageNumbersWarning
	}
	return ""
}

var _ DocumentExtractor = (*Extractor)(nil)
