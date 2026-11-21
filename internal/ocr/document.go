package ocr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"activity-descriptor/internal/fgos"
)

// Document — готовый OCR-пайплайн: текстовый слой PDF читает internal/fgos,
// страницы короче fgos.MinPageTextRunes рендерит Renderer и распознаёт Engine.
// Изображения PNG/JPEG распознаются целиком.
//
// Document реализует fgos.OCRExtractor. Поля можно переопределять в тестах:
// Engine и Renderer — интерфейсы, поэтому внешние бинари не нужны.
type Document struct {
	// Enabled включает OCR. Выключенный OCR возвращает ErrDisabled с
	// инструкцией, но не мешает PDF с полным текстовым слоем.
	Enabled bool
	// Lang — языки tesseract (по умолчанию rus+eng).
	Lang string
	// MaxPages — максимум страниц, которые разрешено OCR-ить за документ.
	MaxPages int
	// Timeout — общий таймаут OCR документа.
	Timeout time.Duration
	// Engine распознаёт изображения (обычно *Tesseract).
	Engine Engine
	// Renderer рендерит страницы PDF (обычно *PDFRenderer).
	Renderer Renderer
}

// NewDocument собирает Document из конфигурации с боевыми Tesseract и
// PDFRenderer.
func NewDocument(cfg Config) *Document {
	cfg = cfg.withDefaults()
	return &Document{
		Enabled:  cfg.Enabled,
		Lang:     cfg.Lang,
		MaxPages: cfg.MaxPages,
		Timeout:  cfg.Timeout,
		Engine:   &Tesseract{Path: cfg.TesseractPath, Lang: cfg.Lang, Timeout: cfg.PageTimeout},
		Renderer: &PDFRenderer{Path: cfg.PDFToPPMPath, DPI: cfg.DPI},
	}
}

// NewDocumentFromEnv собирает Document из переменных OCR_*.
func NewDocumentFromEnv() *Document {
	return NewDocument(ConfigFromEnv())
}

// Available проверяет, что tesseract и pdftoppm доступны. Вызывающий код может
// залогировать предупреждение на старте, не дожидаясь первого скана.
func (d *Document) Available() error {
	if d == nil {
		return disabledError("OCR не настроен")
	}
	return d.checkAvailable(true)
}

// PagesFromPDF возвращает страницы PDF: текстовый слой сохраняется, страницы
// короче fgos.MinPageTextRunes распознаются OCR. Если текстового слоя нет ни на
// одной странице, OCR-ятся все страницы. Число распознаваемых страниц
// ограничено MaxPages, общее время — Timeout.
func (d *Document) PagesFromPDF(ctx context.Context, path string) ([]fgos.Page, error) {
	if d == nil {
		return nil, disabledError("OCR не настроен")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("OCR: не указан путь к PDF")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("OCR: не удалось прочитать PDF: %w", err)
	}
	pages, err := fgos.ExtractPDFPageTexts(data)
	if err != nil {
		return nil, err
	}
	needed := pagesNeedingOCR(pages)
	if len(needed) == 0 {
		// В документе есть полноценный текстовый слой: OCR не нужен и не
		// требует ни включения, ни установленных инструментов.
		return pages, nil
	}
	if !d.enabled() {
		return nil, disabledError(fmt.Sprintf("в PDF %d страниц без текстового слоя", len(needed)))
	}
	if limit := d.maxPages(); len(needed) > limit {
		return nil, fmt.Errorf(
			"%w: нужно распознать %d страниц, лимит %d. Разделите документ или увеличьте %s",
			ErrPageLimit, len(needed), limit, EnvMaxPages)
	}
	if err = d.checkAvailable(true); err != nil {
		return nil, err
	}

	ocrCtx, cancel := context.WithTimeout(ctx, d.timeout())
	defer cancel()
	recognized, err := d.recognizePDFPages(ocrCtx, path, needed)
	if err != nil {
		return nil, err
	}
	merged := mergePages(pages, recognized)
	if len(merged) == 0 {
		return nil, fmt.Errorf("%w: tesseract не нашёл текст ни на одной странице PDF", ErrNoText)
	}
	return merged, nil
}

// RecognizeImage распознаёт PNG/JPEG и возвращает одну страницу. Пустой ответ
// tesseract — ErrNoText, отсутствие бинаря — ErrUnavailable.
func (d *Document) RecognizeImage(ctx context.Context, imagePath string) ([]fgos.Page, error) {
	if d == nil {
		return nil, disabledError("OCR не настроен")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	extension := strings.ToLower(filepath.Ext(strings.TrimSpace(imagePath)))
	switch extension {
	case ".png", ".jpg", ".jpeg":
	default:
		return nil, fmt.Errorf("%w: %q (поддерживаются PNG и JPEG)", ErrUnsupportedImage, extension)
	}
	if !d.enabled() {
		return nil, disabledError("изображение требует OCR")
	}
	if err := d.checkAvailable(false); err != nil {
		return nil, err
	}
	ocrCtx, cancel := context.WithTimeout(ctx, d.timeout())
	defer cancel()
	text, err := d.Engine.Recognize(ocrCtx, imagePath, d.lang())
	if err != nil {
		if ctxErr := ocrCtx.Err(); ctxErr != nil {
			return nil, d.contextError(ctxErr)
		}
		if errors.Is(err, ErrNoText) {
			return nil, fmt.Errorf("%w: изображение %s", ErrNoText, filepath.Base(imagePath))
		}
		return nil, fmt.Errorf("OCR: изображение: %w", err)
	}
	text = cleanText(text)
	if text == "" {
		return nil, fmt.Errorf("%w: изображение %s", ErrNoText, filepath.Base(imagePath))
	}
	return []fgos.Page{{Number: 1, Text: text, OCR: true}}, nil
}

// recognizePDFPages рендерит и распознаёт только нужные страницы, группируя
// соседние номера в диапазоны: один запуск pdftoppm на диапазон.
func (d *Document) recognizePDFPages(ctx context.Context, pdfPath string, needed []int) (map[int]string, error) {
	recognized := make(map[int]string, len(needed))
	for _, span := range pageRanges(needed) {
		paths, cleanup, err := d.Renderer.Render(ctx, pdfPath, span.first, span.last)
		if err != nil {
			if cleanup != nil {
				cleanup()
			}
			if errors.Is(err, ErrToolNotFound) {
				return nil, unavailableError(err)
			}
			return nil, d.wrapContextError(ctx, fmt.Errorf("не удалось отрендерить страницы %d–%d: %w", span.first, span.last, err))
		}
		expected := span.last - span.first + 1
		if len(paths) != expected {
			if cleanup != nil {
				cleanup()
			}
			return nil, fmt.Errorf("OCR: pdftoppm вернул %d изображений для страниц %d–%d (ожидалось %d)",
				len(paths), span.first, span.last, expected)
		}
		for index, imagePath := range paths {
			number := span.first + index
			text, recognizeErr := d.Engine.Recognize(ctx, imagePath, d.lang())
			if recognizeErr != nil {
				if errors.Is(recognizeErr, ErrNoText) {
					// Пустая страница (например, титульный лист без текста):
					// не валим весь документ, просто не заменяем страницу.
					continue
				}
				if cleanup != nil {
					cleanup()
				}
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, d.contextError(ctxErr)
				}
				return nil, fmt.Errorf("страница %d: %w", number, recognizeErr)
			}
			text = cleanText(text)
			if text == "" {
				continue
			}
			recognized[number] = text
		}
		if cleanup != nil {
			cleanup()
		}
	}
	return recognized, nil
}

// mergePages подставляет распознанный текст и помечает страницы Page.OCR.
// Страницы, которые OCR не заменил, остаются с текстовым слоем (даже коротким);
// полностью пустые страницы отбрасываются, как и раньше.
func mergePages(pages []fgos.Page, recognized map[int]string) []fgos.Page {
	merged := make([]fgos.Page, 0, len(pages))
	for _, page := range pages {
		if text, ok := recognized[page.Number]; ok {
			merged = append(merged, fgos.Page{Number: page.Number, Text: text, OCR: true})
			continue
		}
		if strings.TrimSpace(page.Text) != "" {
			merged = append(merged, page)
		}
	}
	return merged
}

// pagesNeedingOCR возвращает номера страниц короче порога.
func pagesNeedingOCR(pages []fgos.Page) []int {
	needed := make([]int, 0, len(pages))
	for _, page := range pages {
		if len([]rune(page.Text)) < fgos.MinPageTextRunes {
			needed = append(needed, page.Number)
		}
	}
	return needed
}

type pageSpan struct {
	first int
	last  int
}

// pageRanges склеивает соседние номера страниц, чтобы не запускать pdftoppm
// отдельно на каждую страницу.
func pageRanges(numbers []int) []pageSpan {
	spans := make([]pageSpan, 0, len(numbers))
	for _, number := range numbers {
		if len(spans) > 0 && number == spans[len(spans)-1].last+1 {
			spans[len(spans)-1].last = number
			continue
		}
		spans = append(spans, pageSpan{first: number, last: number})
	}
	return spans
}

func (d *Document) checkAvailable(needRenderer bool) error {
	if d.Engine == nil {
		return unavailableError(errors.New("движок OCR не настроен"))
	}
	if err := available(d.Engine); err != nil {
		return unavailableError(err)
	}
	if !needRenderer {
		return nil
	}
	if d.Renderer == nil {
		return unavailableError(errors.New("рендерер PDF не настроен"))
	}
	if err := available(d.Renderer); err != nil {
		return unavailableError(err)
	}
	return nil
}

type availability interface {
	Available() error
}

// available проверяет инструмент, если он умеет себя проверять; фейки в тестах
// могут не реализовывать Available.
func available(value any) error {
	if checker, ok := value.(availability); ok {
		return checker.Available()
	}
	return nil
}

func (d *Document) wrapContextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return d.contextError(ctxErr)
	}
	return err
}

func (d *Document) contextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w (%s): %w", ErrTimeout, d.timeout(), context.DeadlineExceeded)
	}
	return fmt.Errorf("OCR: распознавание прервано: %w", err)
}

func (d *Document) enabled() bool {
	return d != nil && d.Enabled
}

func (d *Document) lang() string {
	if d != nil && strings.TrimSpace(d.Lang) != "" {
		return strings.TrimSpace(d.Lang)
	}
	return DefaultLang
}

func (d *Document) maxPages() int {
	if d != nil && d.MaxPages > 0 {
		return d.MaxPages
	}
	return DefaultMaxPages
}

func (d *Document) timeout() time.Duration {
	if d != nil && d.Timeout > 0 {
		return d.Timeout
	}
	return DefaultTimeout
}

var _ fgos.OCRExtractor = (*Document)(nil)
