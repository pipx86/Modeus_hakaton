// Package ocr распознаёт сканированные PDF и изображения для импорта программ
// ФГОС. Текстовый слой PDF читает internal/fgos; если его нет или страница
// почти пустая, страница рендерится в PNG через poppler (pdftoppm) и
// распознаётся tesseract. Внешние программы не обязательны для сборки и
// тестов: Engine и Renderer — интерфейсы, а отсутствие бинарей превращается в
// понятную ошибку с инструкцией по установке, а не в панику.
package ocr

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Значения по умолчанию для OCR. Их можно переопределить переменными
// окружения (см. ConfigFromEnv) или полями Config.
const (
	// DefaultTesseract — имя исполняемого файла tesseract в PATH.
	DefaultTesseract = "tesseract"
	// DefaultPDFToPPM — имя pdftoppm из poppler в PATH.
	DefaultPDFToPPM = "pdftoppm"
	// DefaultLang — языки tesseract: русский и английский.
	DefaultLang = "rus+eng"
	// DefaultDPI — разрешение рендеринга PDF. 200 DPI — компромисс между
	// качеством распознавания и временем: 300 DPI заметно медленнее.
	DefaultDPI = 200
	// DefaultPageTimeout ограничивает распознавание одной страницы.
	DefaultPageTimeout = 60 * time.Second
	// DefaultMaxPages ограничивает число страниц, которые разрешено OCR-ить
	// за один документ: скан на 300 страниц слишком дорог для синхронного
	// импорта.
	DefaultMaxPages = 20
	// DefaultTimeout ограничивает весь OCR документа.
	DefaultTimeout = 5 * time.Minute
)

// Имена переменных окружения. Пути к бинарям важны на Windows: winget ставит
// tesseract и poppler вне PATH рабочего процесса.
const (
	EnvEnabled      = "OCR_ENABLED"
	EnvTesseract    = "OCR_TESSERACT"
	EnvPDFToPPM     = "OCR_PDFTOPPM"
	EnvLang         = "OCR_LANG"
	EnvDPI          = "OCR_DPI"
	EnvPageTimeout  = "OCR_PAGE_TIMEOUT_S"
	EnvMaxPages     = "OCR_MAX_PAGES"
	EnvTotalTimeout = "OCR_TIMEOUT_S"
)

// DocsPath — документация по установке и настройке OCR.
const DocsPath = "docs/mvp/14-ocr.md"

// Ошибки пакета. Вызывающий код матчит их через errors.Is, чтобы выбрать
// HTTP-код и подсказку (422 для выключенного OCR, 503 для недоступных
// инструментов).
var (
	// ErrDisabled — OCR выключен (OCR_ENABLED=false), а документ без него не
	// разобрать.
	ErrDisabled = errors.New("OCR выключен")
	// ErrUnavailable — не найдены tesseract или pdftoppm.
	ErrUnavailable = errors.New("OCR недоступен")
	// ErrToolNotFound — конкретный внешний инструмент не найден в PATH.
	ErrToolNotFound = errors.New("внешний инструмент OCR не найден")
	// ErrNoText — tesseract не вернул текст (пустая страница или плохое
	// качество скана).
	ErrNoText = errors.New("OCR не распознал текст")
	// ErrPageLimit — документ требует OCR больше страниц, чем OCR_MAX_PAGES.
	ErrPageLimit = errors.New("OCR: превышен лимит страниц")
	// ErrTimeout — OCR не уложился в отведённое время.
	ErrTimeout = errors.New("OCR: превышен таймаут")
	// ErrUnsupportedImage — изображение не PNG/JPEG.
	ErrUnsupportedImage = errors.New("OCR: неподдерживаемый формат изображения")
)

// Config — настройки OCR. Нулевые значения заменяются на Default*.
type Config struct {
	// Enabled включает OCR. По умолчанию выключен: без tesseract и poppler
	// сканы отклоняются с инструкцией, а не падают.
	Enabled bool
	// Lang — языки tesseract в формате rus+eng.
	Lang string
	// TesseractPath — путь или имя бинаря tesseract.
	TesseractPath string
	// PDFToPPMPath — путь или имя бинаря pdftoppm.
	PDFToPPMPath string
	// DPI — разрешение рендеринга PDF.
	DPI int
	// PageTimeout — таймаут распознавания одной страницы.
	PageTimeout time.Duration
	// MaxPages — максимум страниц, которые разрешено OCR-ить за документ.
	MaxPages int
	// Timeout — общий таймаут OCR документа.
	Timeout time.Duration
}

// ConfigFromEnv собирает настройки из переменных окружения. Некорректные
// значения не ломают запуск: используется значение по умолчанию.
func ConfigFromEnv() Config {
	return Config{
		Enabled:       envBool(EnvEnabled, false),
		Lang:          envString(EnvLang, DefaultLang),
		TesseractPath: envString(EnvTesseract, DefaultTesseract),
		PDFToPPMPath:  envString(EnvPDFToPPM, DefaultPDFToPPM),
		DPI:           envInt(EnvDPI, DefaultDPI),
		PageTimeout:   envSeconds(EnvPageTimeout, DefaultPageTimeout),
		MaxPages:      envInt(EnvMaxPages, DefaultMaxPages),
		Timeout:       envSeconds(EnvTotalTimeout, DefaultTimeout),
	}
}

// withDefaults нормализует значения: пустые строки, нули и отрицательные
// значения превращаются в значения по умолчанию.
func (c Config) withDefaults() Config {
	if strings.TrimSpace(c.Lang) == "" {
		c.Lang = DefaultLang
	}
	if strings.TrimSpace(c.TesseractPath) == "" {
		c.TesseractPath = DefaultTesseract
	}
	if strings.TrimSpace(c.PDFToPPMPath) == "" {
		c.PDFToPPMPath = DefaultPDFToPPM
	}
	if c.DPI <= 0 {
		c.DPI = DefaultDPI
	}
	if c.PageTimeout <= 0 {
		c.PageTimeout = DefaultPageTimeout
	}
	if c.MaxPages <= 0 {
		c.MaxPages = DefaultMaxPages
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	return c
}

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(strings.ToLower(raw))
	if err != nil {
		return fallback
	}
	return value
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

// envSeconds принимает целое число секунд (60) или duration с единицами (60s).
func envSeconds(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if value, err := time.ParseDuration(raw); err == nil && value > 0 {
		return value
	}
	return fallback
}

// cleanText нормализует вывод tesseract: убирает form feed и \r, обрезает
// хвостовые пробелы и лишние пустые строки.
func cleanText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\f", "\n")
	if strings.ContainsRune(text, '\x00') {
		text = strings.ReplaceAll(text, "\x00", "")
	}
	lines := strings.Split(text, "\n")
	cleaned := lines[:0]
	empty := 0
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			empty++
			if empty > 1 {
				continue
			}
		} else {
			empty = 0
		}
		cleaned = append(cleaned, line)
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

// truncateRunes обрезает длинный stderr до limit рун: в ошибку не нужно
// копировать весь вывод tesseract.
func truncateRunes(text string, limit int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return "…" + string(runes[len(runes)-limit:])
}

const (
	tesseractInstallHint = "winget install --id UB-Mannheim.TesseractOCR -e"
	popplerInstallHint   = "winget install --id oschwartz10612.Poppler -e"
	tesseractPathExample = `C:\Program Files\Tesseract-OCR\tesseract.exe`
	popplerPathExample   = `%LOCALAPPDATA%\Microsoft\WinGet\Packages\oschwartz10612.Poppler_*\poppler-*\Library\bin\pdftoppm.exe`
)

// toolNotFoundError возвращает понятную ошибку с командой установки и именем
// переменной окружения, которой задаётся путь.
func toolNotFoundError(binary, envName, installHint, pathExample string) error {
	return fmt.Errorf(
		"%w: %q не найден. Установите: %s; затем задайте %s (например, %s) или добавьте каталог в PATH. Подробности: %s",
		ErrToolNotFound, binary, installHint, envName, pathExample, DocsPath)
}

// unavailableError оборачивает ошибку инструмента, сохраняя и ErrUnavailable,
// и исходную ErrToolNotFound для errors.Is.
func unavailableError(err error) error {
	return fmt.Errorf("%w: %w. Проверьте OCR_TESSERACT/OCR_PDFTOPPM и PATH; подробности: %s",
		ErrUnavailable, err, DocsPath)
}

// disabledError объясняет, как включить OCR, и сохраняет ErrDisabled.
func disabledError(reason string) error {
	return fmt.Errorf(
		"%w: %s. Включите %s=true, установите tesseract (%s) и poppler (%s), задайте пути %s/%s. Подробности: %s",
		ErrDisabled, reason, EnvEnabled, tesseractInstallHint, popplerInstallHint,
		EnvTesseract, EnvPDFToPPM, DocsPath)
}
