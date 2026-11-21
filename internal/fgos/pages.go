package fgos

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
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
	// minTextRunes — порог «документ действительно содержит текстовый слой».
	minTextRunes = 20
	// maxDOCXDecompressed защищает от zip-бомб в DOCX.
	maxDOCXDecompressed = 64 << 20
)

// ExtractPages возвращает страницы PDF или DOCX по байтам файла. Формат
// определяется по расширению name: .pdf или .docx.
//
// Для PDF страницы берутся из дерева страниц библиотеки (номера точные). Для
// DOCX точных номеров нет: страницы восстанавливаются по разрывам, поэтому
// вызывающий код добавляет предупреждение ApproximatePageNumbersWarning.
func ExtractPages(name string, data []byte) ([]Page, error) {
	if len(data) == 0 {
		return nil, errors.New("fgos: файл пустой")
	}
	if len(data) > ingestion.MaxDocumentSize {
		return nil, fmt.Errorf("fgos: файл больше %d МБ", ingestion.MaxDocumentSize>>20)
	}
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(name))) {
	case ".pdf":
		return extractPDFPages(data)
	case ".docx":
		return extractDOCXPages(data)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedFormat, strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), "."))
	}
}

// ExtractPagesFile читает файл с диска и возвращает страницы документа.
func ExtractPagesFile(path string) ([]Page, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fgos: не удалось прочитать файл: %w", err)
	}
	return ExtractPages(path, data)
}

// extractPDFPages читает текст постранично. Библиотека паникует на
// повреждённых файлах, поэтому паника превращается в понятную ошибку.
func extractPDFPages(data []byte) (pages []Page, err error) {
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
	textRunes := 0
	for number := 1; number <= total; number++ {
		page := reader.Page(number)
		text, pageErr := page.GetPlainText(nil)
		if pageErr != nil {
			// Одна повреждённая страница не должна ронять весь документ.
			// Если текста нет нигде, ниже вернётся ErrNoTextLayer.
			continue
		}
		cleaned := cleanPageText(text)
		if cleaned == "" {
			continue
		}
		textRunes += len([]rune(cleaned))
		if textRunes > maxPageTextRunes {
			return nil, fmt.Errorf("fgos: текст PDF больше %d символов", maxPageTextRunes)
		}
		pages = append(pages, Page{Number: number, Text: cleaned})
	}
	if textRunes < minTextRunes {
		return nil, fmt.Errorf("%w: в PDF не найден текст", ErrNoTextLayer)
	}
	return pages, nil
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
	if textRunes < minTextRunes {
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
