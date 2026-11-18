package ingestion

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// MaxDocumentSize ограничивает размер загружаемого файла.
const MaxDocumentSize = 20 << 20

// MaxExtractedText ограничивает объём текста, который сохраняется как источник.
const MaxExtractedText = 200_000

// ExtractDocumentText извлекает текст из PDF, DOCX и простых текстовых форматов.
//
// Поддерживаются .pdf, .docx, .txt, .md, .csv и .json. Ошибка возвращается
// человекочитаемая: её показывает админка при загрузке материала.
func ExtractDocumentText(filename string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("файл пустой")
	}
	if len(data) > MaxDocumentSize {
		return "", fmt.Errorf("файл больше %d МБ", MaxDocumentSize>>20)
	}

	ext := strings.ToLower(filepath.Ext(filename))
	var (
		text string
		err  error
	)
	switch ext {
	case ".pdf":
		text, err = extractPDF(data)
	case ".docx":
		text, err = extractDOCX(data)
	case ".txt", ".md", ".markdown", ".csv", ".json":
		text, err = extractPlainText(data)
	default:
		return "", fmt.Errorf("формат %q не поддерживается: загрузите PDF, DOCX, TXT, MD, CSV или JSON", ext)
	}
	if err != nil {
		return "", err
	}

	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return "", errors.New("в файле не найден текст: для скана PDF нужно распознавание")
	}
	if !utf8.ValidString(text) {
		return "", errors.New("текст файла не в кодировке UTF-8")
	}
	if len([]rune(text)) > MaxExtractedText {
		runes := []rune(text)
		text = string(runes[:MaxExtractedText])
	}
	return text, nil
}

func extractPlainText(data []byte) (string, error) {
	text := strings.TrimPrefix(string(data), "\uFEFF")
	if !utf8.ValidString(text) {
		return "", errors.New("текст файла не в кодировке UTF-8")
	}
	return text, nil
}

func extractPDF(data []byte) (text string, err error) {
	// Библиотека паникует на повреждённых PDF, поэтому ловим это и отдаём понятную ошибку.
	defer func() {
		if recover() != nil {
			text = ""
			err = errors.New("не удалось разобрать PDF: файл повреждён или защищён")
		}
	}()
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("PDF: %w", err)
	}
	plain, err := reader.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("текст PDF: %w", err)
	}
	content, err := io.ReadAll(io.LimitReader(plain, MaxExtractedText*4))
	if err != nil {
		return "", fmt.Errorf("чтение текста PDF: %w", err)
	}
	return string(content), nil
}

// extractDOCX читает word/document.xml и собирает текст абзацев.
func extractDOCX(data []byte) (string, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", errors.New("не удалось открыть DOCX: файл повреждён")
	}
	var document *zip.File
	for _, file := range archive.File {
		if file.Name == "word/document.xml" {
			document = file
			break
		}
	}
	if document == nil {
		return "", errors.New("в DOCX нет word/document.xml")
	}
	stream, err := document.Open()
	if err != nil {
		return "", fmt.Errorf("DOCX: %w", err)
	}
	defer stream.Close()

	decoder := xml.NewDecoder(stream)
	var builder strings.Builder
	inText := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("DOCX: %w", err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "t":
				inText = true
			case "tab":
				builder.WriteString("\t")
			case "br":
				builder.WriteString("\n")
			}
		case xml.EndElement:
			switch element.Name.Local {
			case "t":
				inText = false
			case "p":
				builder.WriteString("\n")
			}
		case xml.CharData:
			if inText {
				builder.Write(element)
			}
		}
	}
	return builder.String(), nil
}
