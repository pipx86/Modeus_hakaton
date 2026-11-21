package fgos

import (
	"activity-descriptor/internal/tempdir"
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"activity-descriptor/internal/ingestion"

	"github.com/ledongthuc/pdf"
)

const (
	// maxPageTextRunes ограничивает суммарный текст, который адаптер держит в
	// памяти: 20 МБ файла достаточно для 100+ страниц, но не для бесконечного
	// потока.
	maxPageTextRunes = 2_000_000
	// MinPageTextRunes — порог «документ действительно содержит текстовый
	// слой» и «страница не пустая». Страницы короче порога может заменить OCR:
	// в сканах заголовки и колонтитулы дают 1–2 символа текстового слоя.
	MinPageTextRunes = 20
	// maxDOCXDecompressed защищает от zip-бомб в DOCX.
	maxDOCXDecompressed = 64 << 20
)

// ExtractPages возвращает страницы PDF или DOCX по байтам файла. Формат
// определяется по расширению name: .pdf, .docx, .png, .jpg или .jpeg.
//
// Для PDF страницы берутся из дерева страниц библиотеки (номера точные). Для
// DOCX точных номеров нет: страницы восстанавливаются по разрывам, поэтому
// вызывающий код добавляет предупреждение ApproximatePageNumbersWarning.
//
// Функция не использует OCR: PDF без текстового слоя и изображения возвращают
// понятную ошибку. Для сканов используйте ExtractPagesWithOCR.
func ExtractPages(name string, data []byte) ([]Page, error) {
	return ExtractPagesWithOCR(context.Background(), name, data, nil)
}

// ExtractPagesWithOCR — ExtractPages с OCR для страниц без текстового слоя.
// ocr == nil сохраняет прежнее поведение (ErrNoTextLayer на скане).
//
// Если установлен OCR, страницы PDF короче MinPageTextRunes распознаются через
// OCRExtractor.PagesFromPDF; изображения PNG/JPEG распознаются целиком через
// OCRExtractor.RecognizeImage.
func ExtractPagesWithOCR(ctx context.Context, name string, data []byte, ocr OCRExtractor) ([]Page, error) {
	if len(data) == 0 {
		return nil, errors.New("fgos: файл пустой")
	}
	if len(data) > ingestion.MaxDocumentSize {
		return nil, fmt.Errorf("fgos: файл больше %d МБ", ingestion.MaxDocumentSize>>20)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	extension := strings.ToLower(filepath.Ext(strings.TrimSpace(name)))
	switch extension {
	case ".pdf":
		return extractPDFPages(ctx, data, ocr)
	case ".docx":
		return extractDOCXPages(data)
	case ".png", ".jpg", ".jpeg":
		return extractImagePages(ctx, data, extension, ocr)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedFormat, strings.TrimPrefix(extension, "."))
	}
}

// ExtractPagesFile читает файл с диска и возвращает страницы документа без
// OCR; для сканов используйте ExtractPagesWithOCR.
func ExtractPagesFile(path string) ([]Page, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fgos: не удалось прочитать файл: %w", err)
	}
	return ExtractPages(path, data)
}

// extractPDFPages читает текст постранично. Если хотя бы одна страница почти
// пустая и передан OCR, документ распознаётся через OCRExtractor (он сам
// сохраняет страницы с текстовым слоем). Библиотека паникует на повреждённых
// файлах, поэтому паника превращается в понятную ошибку.
func extractPDFPages(ctx context.Context, data []byte, ocr OCRExtractor) ([]Page, error) {
	pages, err := ExtractPDFPageTexts(data)
	if err != nil {
		return nil, err
	}
	if ocr != nil && pagesNeedOCR(pages) {
		recognized, ocrErr := pagesWithOCR(ctx, ".pdf", data, ocr)
		if ocrErr != nil {
			return nil, fmt.Errorf("fgos: PDF: %w", ocrErr)
		}
		pages = recognized
	}
	return finalizePages(pages, "PDF", MinPageTextRunes)
}

// popplerBinary возвращает путь к pdftotext. Переменная FGOS_PDFTOTEXT задаёт
// явный путь, "off"/"false" отключают poppler, пустое значение — поиск в PATH.
func popplerBinary() string {
	configured := strings.TrimSpace(os.Getenv("FGOS_PDFTOTEXT"))
	if strings.EqualFold(configured, "off") || strings.EqualFold(configured, "false") {
		return ""
	}
	candidate := configured
	if candidate == "" {
		candidate = "pdftotext"
	}
	if path, err := exec.LookPath(candidate); err == nil {
		return path
	}
	if configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return configured
		}
	}
	return ""
}

// extractPDFPagesWithPoppler читает текст через pdftotext. Встроенная
// библиотека на части современных PDF отдаёт нечитаемый текст (CID-шрифты),
// из-за чего модель не находит компетенции; poppler сохраняет порядок строк
// и кириллицу. ok=false означает «не получилось, используй прежний путь».
func extractPDFPagesWithPoppler(data []byte) ([]Page, bool) {
	binary := popplerBinary()
	if binary == "" || len(data) == 0 {
		return nil, false
	}
	directory, err := tempdir.Create("fgos-pdf-*")
	if err != nil {
		return nil, false
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "document.pdf")
	if err = os.WriteFile(path, data, 0o600); err != nil {
		return nil, false
	}
	command := exec.Command(binary, "-layout", "-enc", "UTF-8", path, "-")
	output, err := command.Output()
	if err != nil {
		return nil, false
	}
	pages := splitPopplerPages(string(output))
	if len(pages) == 0 {
		return nil, false
	}
	hasText := false
	for _, page := range pages {
		if strings.TrimSpace(page.Text) != "" {
			hasText = true
			break
		}
	}
	if !hasText {
		return nil, false
	}
	return pages, true
}

// splitPopplerPages режет вывод pdftotext по разделителю страниц (form feed).
// Пустые страницы сохраняются, чтобы номера совпадали с PDF.
func splitPopplerPages(raw string) []Page {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, "\f")
	for len(parts) > 0 && strings.TrimSpace(parts[len(parts)-1]) == "" {
		parts = parts[:len(parts)-1]
	}
	pages := make([]Page, 0, len(parts))
	for index, part := range parts {
		pages = append(pages, Page{Number: index + 1, Text: cleanPageText(part)})
	}
	return pages
}

// ExtractPDFPageTexts возвращает по одной странице на каждую страницу PDF,
// включая страницы без текстового слоя (Text пустой). Ошибка ErrNoTextLayer
// здесь не возвращается: решение об OCR принимает вызывающий код.
func ExtractPDFPageTexts(data []byte) (pages []Page, err error) {
	if popplerPages, ok := extractPDFPagesWithPoppler(data); ok {
		return popplerPages, nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			pages = nil
			err = fmt.Errorf("%w: PDF повреждён или защищён", ErrInvalidDocument)
		}
	}()
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: PDF: %v", ErrInvalidDocument, err)
	}
	total := reader.NumPage()
	if total <= 0 {
		return nil, fmt.Errorf("%w: в PDF нет страниц", ErrNoTextLayer)
	}
	pages = make([]Page, 0, total)
	for number := 1; number <= total; number++ {
		page := reader.Page(number)
		text, pageErr := page.GetPlainText(nil)
		if pageErr != nil {
			// Одна повреждённая страница не должна ронять весь документ:
			// страница останется пустой и попадёт в OCR.
			pages = append(pages, Page{Number: number})
			continue
		}
		pages = append(pages, Page{Number: number, Text: cleanPageText(text)})
	}
	return pages, nil
}

// pagesNeedOCR сообщает, есть ли в документе страницы короче MinPageTextRunes.
// Пустой результат (нет страниц) тоже требует OCR — но PDF без страниц
// отсекается раньше.
func pagesNeedOCR(pages []Page) bool {
	if len(pages) == 0 {
		return true
	}
	for _, page := range pages {
		if len([]rune(page.Text)) < MinPageTextRunes {
			return true
		}
	}
	return false
}

// pagesWithOCR сохраняет данные во временный файл (OCR-инструменты работают с
// путями) и передаёт его OCRExtractor. Каталог удаляется после возврата.
func pagesWithOCR(ctx context.Context, extension string, data []byte, ocr OCRExtractor) ([]Page, error) {
	directory, err := tempdir.Create("fgos-ocr-*")
	if err != nil {
		return nil, fmt.Errorf("не удалось создать временный файл для OCR: %w", err)
	}
	defer func() { _ = os.RemoveAll(directory) }()
	path := filepath.Join(directory, "document"+extension)
	if writeErr := os.WriteFile(path, data, 0o600); writeErr != nil {
		return nil, fmt.Errorf("не удалось записать временный файл для OCR: %w", writeErr)
	}
	return ocr.PagesFromPDF(ctx, path)
}

// extractImagePages распознаёт изображение через OCR. Без OCR изображения
// отклоняются как неподдерживаемый формат: детерминированного парсера у них нет.
func extractImagePages(ctx context.Context, data []byte, extension string, ocr OCRExtractor) ([]Page, error) {
	if ocr == nil {
		return nil, fmt.Errorf("%w: изображение (%s) требует OCR: включите OCR и установите tesseract",
			ErrUnsupportedFormat, strings.TrimPrefix(extension, "."))
	}
	directory, err := tempdir.Create("fgos-ocr-image-*")
	if err != nil {
		return nil, fmt.Errorf("fgos: не удалось создать временный файл для OCR: %w", err)
	}
	defer func() { _ = os.RemoveAll(directory) }()
	path := filepath.Join(directory, "image"+extension)
	if writeErr := os.WriteFile(path, data, 0o600); writeErr != nil {
		return nil, fmt.Errorf("fgos: не удалось записать временный файл для OCR: %w", writeErr)
	}
	pages, ocrErr := ocr.RecognizeImage(ctx, path)
	if ocrErr != nil {
		return nil, fmt.Errorf("fgos: изображение: %w", ocrErr)
	}
	return finalizePages(pages, "изображении", 1)
}

// finalizePages убирает пустые страницы, проверяет суммарный объём текста и
// минимальный порог. Для PDF порог MinPageTextRunes отделяет скан от документа
// с текстовым слоем; для изображения достаточно непустого текста.
func finalizePages(pages []Page, format string, minRunes int) ([]Page, error) {
	filtered := make([]Page, 0, len(pages))
	textRunes := 0
	for _, page := range pages {
		if strings.TrimSpace(page.Text) == "" {
			continue
		}
		textRunes += len([]rune(page.Text))
		if textRunes > maxPageTextRunes {
			return nil, fmt.Errorf("fgos: текст %s больше %d символов", format, maxPageTextRunes)
		}
		filtered = append(filtered, page)
	}
	if textRunes < minRunes {
		return nil, fmt.Errorf("%w: в %s не найден текст", ErrNoTextLayer, format)
	}
	return filtered, nil
}

// extractDOCXPages читает word/document.xml и делит текст по разрывам страниц:
// w:br w:type="page", w:lastRenderedPageBreak и w:pageBreakBefore. Нумерация
// приблизительная — Word расставляет разрывы при вёрстке, а не хранит их.
func extractDOCXPages(data []byte) ([]Page, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: DOCX повреждён", ErrInvalidDocument)
	}
	var document *zip.File
	for _, file := range archive.File {
		if file.Name == "word/document.xml" {
			document = file
			break
		}
	}
	if document == nil {
		return nil, fmt.Errorf("%w: в DOCX нет word/document.xml", ErrInvalidDocument)
	}
	stream, err := document.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: DOCX: %v", ErrInvalidDocument, err)
	}
	defer func() { _ = stream.Close() }()

	limited := &io.LimitedReader{R: stream, N: maxDOCXDecompressed + 1}
	decoder := xml.NewDecoder(limited)

	pages := []Page{}
	var current strings.Builder
	flush := func() {
		text := cleanPageText(current.String())
		current.Reset()
		if text == "" {
			return
		}
		pages = append(pages, Page{Number: len(pages) + 1, Text: text})
	}
	inText := false
	for {
		token, tokenErr := decoder.Token()
		if tokenErr == io.EOF {
			break
		}
		if tokenErr != nil {
			return nil, fmt.Errorf("%w: DOCX: %v", ErrInvalidDocument, tokenErr)
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "t":
				inText = true
			case "tab":
				current.WriteString("\t")
			case "br":
				// w:br бывает обычным переносом строки и разрывом страницы.
				if attributeValue(element.Attr, "type") == "page" {
					flush()
				} else {
					current.WriteString("\n")
				}
			case "lastRenderedPageBreak", "pageBreakBefore":
				flush()
			}
		case xml.EndElement:
			switch element.Name.Local {
			case "t":
				inText = false
			case "p":
				current.WriteString("\n")
			}
		case xml.CharData:
			if inText {
				current.Write(element)
			}
		}
	}
	flush()
	if limited.N <= 0 {
		return nil, fmt.Errorf("%w: DOCX: распакованный документ слишком большой", ErrInvalidDocument)
	}
	textRunes := 0
	for _, page := range pages {
		textRunes += len([]rune(page.Text))
	}
	if textRunes < MinPageTextRunes {
		return nil, fmt.Errorf("%w: в DOCX не найден текст", ErrNoTextLayer)
	}
	if textRunes > maxPageTextRunes {
		return nil, fmt.Errorf("fgos: текст DOCX больше %d символов", maxPageTextRunes)
	}
	return pages, nil
}

func attributeValue(attributes []xml.Attr, name string) string {
	for _, attribute := range attributes {
		if strings.EqualFold(attribute.Name.Local, name) {
			return strings.TrimSpace(attribute.Value)
		}
	}
	return ""
}

// cleanPageText нормализует переводы строк и убирает невалидный UTF-8, который
// встречается в PDF с битыми шрифтами.
func cleanPageText(text string) string {
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "")
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\x00", "")
	return strings.TrimSpace(text)
}
