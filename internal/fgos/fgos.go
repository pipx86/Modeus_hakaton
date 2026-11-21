// Package fgos извлекает компетенции и KSA из документов ФГОС (PDF/DOCX)
// через существующий LLM-клиент. Адаптер ничего не пишет в БД: он возвращает
// ingestion.EducationProgram и предупреждения, а черновик создаёт вызывающий
// код (programwatch.Checker или API импорта программ).
//
// Пайплайн:
//
//  1. ExtractPages режет PDF/DOCX на страницы с текстом;
//  2. Extract отбирает страницы раздела компетенций (SelectCompetencyPages):
//     общие положения, структура программы и требования к условиям реализации
//     не уходят модели, если включён FGOS_SECTION_ONLY (по умолчанию);
//  3. Extract режет отобранные страницы на чанки, не разрывая страницу, и
//     просит модель вернуть JSON с компетенциями и дословными цитатами; чанки
//     обрабатываются пулом воркеров (Concurrency), результаты собираются в
//     порядке страниц;
//  4. при отказе модели (413, context length, таймаут) или невалидном/пустом
//     JSON чанк делится пополам (до 1500 символов, не более четырёх уровней),
//     результаты подзапросов объединяются;
//  5. каждый пункт валидируется (код, тип, цитата на указанной странице);
//     невалидные пункты отбрасываются с предупреждением;
//  6. результаты чанков дедуплицируются по коду компетенции с объединением KSA.
package fgos

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
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
	// DefaultMaxCharsPerChunk — целевой размер чанка. Модели с небольшим
	// контекстом отвечают 413 на длинный запрос, поэтому базовый чанк — 6k
	// символов: он проходит у большинства провайдеров, а при отказе адаптер
	// делит его пополам (см. extractChunk).
	DefaultMaxCharsPerChunk = 6000
	// DefaultTimeout ограничивает весь разбор одного документа.
	DefaultTimeout = 5 * time.Minute
	// DefaultMaxResponseTokens ограничивает ответ модели на один чанк. Если
	// клиент поддерживает SetMaxResponseTokens, лимит передаётся ему; кроме
	// того, адаптер сам отбраковывает слишком длинный ответ как ошибку.
	DefaultMaxResponseTokens = 4000
	// DefaultConcurrency — сколько чанков разбирать одновременно. Модель
	// отвечает 30–60 секунд на чанк, поэтому последовательный разбор большого
	// документа не укладывается в пятиминутный таймаут.
	DefaultConcurrency = 3
	// MaxConcurrency ограничивает параллельность, чтобы не провоцировать 429 у
	// провайдера.
	MaxConcurrency = 6
)

// SplitChunkWarning — предупреждение черновика, когда чанк пришлось уменьшить
// из-за лимита модели (413, context length, таймаут или невалидный JSON).
const SplitChunkWarning = "чанк уменьшен из-за лимита модели"

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
// (с 1). Для DOCX и OCR нумерация приблизительная.
type Page struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
	// OCR — текст получен распознаванием скана: точность ниже, а цитаты могут
	// отличаться от бумажного оригинала.
	OCR bool `json:"ocr,omitempty"`
}

// OCRPageWarning — предупреждение черновика о странице, текст которой получен
// OCR: точность распознавания ниже, цитаты может проверять человек.
func OCRPageWarning(number int) string {
	return fmt.Sprintf("страница %d распознана OCR (точность ниже)", number)
}

// DocumentExtractor — контракт адаптера для programwatch.Checker и API
// импорта программ. В тестах подменяется фейком без сети.
type DocumentExtractor interface {
	ExtractPages(name string, data []byte) ([]Page, error)
	Extract(ctx context.Context, pages []Page) (ingestion.EducationProgram, []string, error)
}

// MaxResponseTokensSetter — необязательное расширение llm.ChatClient: ограничить
// число токенов ответа на один запрос. Клиенты проекта задают MaxTokens при
// создании (LLM_<AGENT>_MAX_TOKENS), поэтому адаптер вызывает сеттер только
// если конкретный клиент его реализует; ограничение применяется один раз на
// разбор документа.
type MaxResponseTokensSetter interface {
	SetMaxResponseTokens(tokens int)
}

// Extractor — LLM-адаптер ФГОС поверх llm.ChatClient. Поля можно менять
// напрямую; нулевые значения заменяются на Default*.
type Extractor struct {
	// Client — существующий LLM-клиент проекта (llm.ChatClient). Обязателен.
	Client llm.ChatClient
	// MaxPages — максимум страниц в документе; превышение даёт ошибку.
	MaxPages int
	// MaxCharsPerChunk — целевой размер чанка в символах. Чанк может быть
	// уменьшен автоматически, если модель отвергла запрос.
	MaxCharsPerChunk int
	// MaxResponseTokens — ограничение ответа модели на один запрос. Отрицательное
	// значение отключает локальную проверку размера ответа.
	MaxResponseTokens int
	// Concurrency — сколько чанков разбирать одновременно. 1 означает
	// последовательную обработку (как раньше). Ноль означает значение из
	// FGOS_CONCURRENCY или DefaultConcurrency.
	Concurrency int
	// Timeout — бюджет времени на весь разбор документа.
	Timeout time.Duration
	// SectionOnly — отправлять модели только страницы раздела компетенций.
	// NewExtractor читает FGOS_SECTION_ONLY (по умолчанию true). Поле можно
	// менять после создания адаптера.
	SectionOnly bool
	// SectionWindow — сколько соседних страниц добавлять к найденным страницам
	// раздела. NewExtractor читает FGOS_SECTION_WINDOW (по умолчанию 1).
	SectionWindow int
	// Logger — необязательный логгер; по умолчанию slog.Default().
	Logger *slog.Logger

	// sectionConfigured показывает, что NewExtractor уже разрешил env-значения.
	// Для Extractor, созданного литералом, значения по-прежнему читаются из env,
	// поэтому по умолчанию отбор включён.
	sectionConfigured bool
}

// NewExtractor создаёт адаптер с боевыми настройками по умолчанию.
// Параллельность читается из FGOS_CONCURRENCY (1 — последовательно, по
// умолчанию 3, не больше 6). Отбор страниц раздела компетенций включается
// FGOS_SECTION_ONLY (по умолчанию true), окно соседних страниц задаёт
// FGOS_SECTION_WINDOW (по умолчанию 1).
func NewExtractor(client llm.ChatClient) *Extractor {
	return &Extractor{
		Client:            client,
		MaxPages:          DefaultMaxPages,
		MaxCharsPerChunk:  DefaultMaxCharsPerChunk,
		MaxResponseTokens: DefaultMaxResponseTokens,
		Concurrency:       concurrencyFromEnv(),
		Timeout:           DefaultTimeout,
		SectionOnly:       sectionOnlyFromEnv(),
		SectionWindow:     sectionWindowFromEnv(),
		sectionConfigured: true,
	}
}

// sectionOnlyFromEnv читает FGOS_SECTION_ONLY. Пустое или некорректное
// значение означает true: по умолчанию модель получает только раздел
// компетенций, а весь документ отправляется лишь при явном false.
func sectionOnlyFromEnv() bool {
	raw := strings.TrimSpace(os.Getenv("FGOS_SECTION_ONLY"))
	if raw == "" {
		return true
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return true
	}
	return value
}

// sectionWindowFromEnv читает FGOS_SECTION_WINDOW. 0 отключает соседние
// страницы, отрицательное или некорректное значение даёт
// DefaultSectionWindow.
func sectionWindowFromEnv() int {
	raw := strings.TrimSpace(os.Getenv("FGOS_SECTION_WINDOW"))
	if raw == "" {
		return DefaultSectionWindow
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return DefaultSectionWindow
	}
	return value
}

// sectionOnly — фактическое решение об отборе: поле адаптера, если оно задано
// NewExtractor, иначе значение FGOS_SECTION_ONLY.
func (e *Extractor) sectionOnly() bool {
	if e == nil || !e.sectionConfigured {
		return sectionOnlyFromEnv()
	}
	return e.SectionOnly
}

// sectionWindow — фактическое окно соседних страниц.
func (e *Extractor) sectionWindow() int {
	if e == nil || !e.sectionConfigured {
		return sectionWindowFromEnv()
	}
	if e.SectionWindow < 0 {
		return DefaultSectionWindow
	}
	return e.SectionWindow
}

// concurrencyFromEnv читает FGOS_CONCURRENCY. Некорректное значение даёт
// DefaultConcurrency, слишком большое ограничивается MaxConcurrency.
func concurrencyFromEnv() int {
	raw := strings.TrimSpace(os.Getenv("FGOS_CONCURRENCY"))
	if raw == "" {
		return DefaultConcurrency
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return DefaultConcurrency
	}
	if value > MaxConcurrency {
		return MaxConcurrency
	}
	return value
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

func (e *Extractor) maxResponseTokens() int {
	if e == nil {
		return DefaultMaxResponseTokens
	}
	if e.MaxResponseTokens < 0 {
		return 0
	}
	if e.MaxResponseTokens > 0 {
		return e.MaxResponseTokens
	}
	return DefaultMaxResponseTokens
}

// concurrency — фактическая параллельность: 1 для последовательного разбора,
// не больше MaxConcurrency.
func (e *Extractor) concurrency() int {
	if e == nil || e.Concurrency <= 0 {
		return DefaultConcurrency
	}
	if e.Concurrency > MaxConcurrency {
		return MaxConcurrency
	}
	return e.Concurrency
}

// maxResponseChars — локальный предел длины ответа в символах: даже если клиент
// не умеет ограничивать max tokens, слишком длинный ответ считается ошибкой и
// чанк делится.
func (e *Extractor) maxResponseChars() int {
	tokens := e.maxResponseTokens()
	if tokens <= 0 {
		return 0
	}
	return tokens * responseCharsPerToken
}

func (e *Extractor) logger() *slog.Logger {
	if e != nil && e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}

// applyResponseTokenLimit передаёт лимит ответа клиенту, если тот поддерживает
// MaxResponseTokensSetter. Реальные клиенты проекта задают max tokens при
// создании, поэтому вызов безвреден и выполняется только при поддержке.
func (e *Extractor) applyResponseTokenLimit() {
	if e == nil || e.Client == nil {
		return
	}
	setter, ok := e.Client.(MaxResponseTokensSetter)
	if !ok {
		return
	}
	tokens := e.maxResponseTokens()
	if tokens <= 0 {
		return
	}
	setter.SetMaxResponseTokens(tokens)
	e.logger().Debug("fgos_response_limit_applied", "max_response_tokens", tokens)
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
