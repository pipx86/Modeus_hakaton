package programdocx

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
)

// Namespace'ы OOXML, используемые в минимальном пакете DOCX.
const (
	namespaceWord          = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	namespaceRelationships = "http://schemas.openxmlformats.org/package/2006/relationships"
	namespaceContentTypes  = "http://schemas.openxmlformats.org/package/2006/content-types"
	namespaceCoreProps     = "http://schemas.openxmlformats.org/package/2006/metadata/core-properties"
	namespaceDC            = "http://purl.org/dc/elements/1.1/"
	namespaceDCTerms       = "http://purl.org/dc/terms/"
	namespaceXSI           = "http://www.w3.org/2001/XMLSchema-instance"
	namespaceExtendedProps = "http://schemas.openxmlformats.org/officeDocument/2006/extended-properties"
	namespaceVT            = "http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes"
)

const (
	// bodyFontSize — 11 pt в half-points, базовый размер документа.
	bodyFontSize = 22
	// titleFontSize — 16 pt: заголовок «Образовательная программа».
	titleFontSize = 32
	// programTitleFontSize — 18 pt: название программы крупнее заголовка.
	programTitleFontSize = 36
	// sectionFontSize — 14 pt: заголовки разделов документа.
	sectionFontSize = 28

	// a4Width/a4Height — размер A4 в twips (210 × 297 мм).
	a4Width  = 11906
	a4Height = 16838
	// pageMargin — поля 2 см в twips (1 см = 567 twips).
	pageMargin = 1134
)

// sectionProperties задаёт A4, поля 2 см и книжную ориентацию. Это последний
// элемент w:body, поэтому параметры применяются ко всему документу.
var sectionProperties = fmt.Sprintf(
	`<w:sectPr><w:pgSz w:w="%d" w:h="%d"/>`+
		`<w:pgMar w:top="%d" w:right="%d" w:bottom="%d" w:left="%d" w:header="708" w:footer="708" w:gutter="0"/>`+
		`<w:cols w:space="708"/>`+
		`<w:docGrid w:linePitch="360"/>`+
		`</w:sectPr>`,
	a4Width, a4Height, pageMargin, pageMargin, pageMargin, pageMargin,
)

// paragraphStyle — параметры абзаца. Нулевые значения заменяются значениями по
// умолчанию: отступ после абзаца 120 twips, межстрочный интервал 276 (1,15).
type paragraphStyle struct {
	align         string
	spaceBefore   int
	spaceAfter    int
	line          int
	keepNext      bool
	indentLeft    int
	indentHanging int
}

// runStyle — параметры текстового фрагмента. size задан в half-points; 0
// означает базовый шрифт 11 pt.
type runStyle struct {
	text   string
	bold   bool
	italic bool
	size   int
}

func renderParagraph(style paragraphStyle, runs ...runStyle) string {
	var b strings.Builder
	b.WriteString("<w:p><w:pPr>")

	// spaceAfter < 0 — явный ноль (плотные абзацы внутри таблицы),
	// spaceAfter == 0 — значение по умолчанию.
	spaceAfter := style.spaceAfter
	switch {
	case spaceAfter < 0:
		spaceAfter = 0
	case spaceAfter == 0:
		spaceAfter = 120
	}
	line := style.line
	if line <= 0 {
		line = 276
	}
	fmt.Fprintf(&b, `<w:spacing w:before="%d" w:after="%d" w:line="%d" w:lineRule="auto"/>`,
		style.spaceBefore, spaceAfter, line)
	if style.keepNext {
		b.WriteString("<w:keepNext/>")
	}
	if style.align != "" {
		fmt.Fprintf(&b, `<w:jc w:val="%s"/>`, style.align)
	}
	if style.indentLeft > 0 || style.indentHanging > 0 {
		fmt.Fprintf(&b, `<w:ind w:left="%d" w:hanging="%d"/>`, style.indentLeft, style.indentHanging)
	}
	b.WriteString("</w:pPr>")

	for _, run := range runs {
		b.WriteString(renderRun(run))
	}
	b.WriteString("</w:p>")
	return b.String()
}

func renderRun(style runStyle) string {
	size := style.size
	if size <= 0 {
		size = bodyFontSize
	}

	var b strings.Builder
	b.WriteString("<w:r><w:rPr>")
	b.WriteString(`<w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:eastAsia="Times New Roman" w:cs="Times New Roman"/>`)
	if style.bold {
		b.WriteString("<w:b/><w:bCs/>")
	}
	if style.italic {
		b.WriteString("<w:i/><w:iCs/>")
	}
	fmt.Fprintf(&b, `<w:sz w:val="%d"/><w:szCs w:val="%d"/>`, size, size)
	b.WriteString(`<w:lang w:val="ru-RU" w:eastAsia="ru-RU"/>`)
	b.WriteString("</w:rPr>")
	fmt.Fprintf(&b, `<w:t xml:space="preserve">%s</w:t>`, escapeXML(cleanXMLText(style.text)))
	b.WriteString("</w:r>")
	return b.String()
}

// escapeXML экранирует текст для вставки в w:t. encoding/xml корректно
// сохраняет кириллицу в UTF-8 и заменяет служебные символы сущностями.
func escapeXML(text string) string {
	var b bytes.Buffer
	// EscapeText в bytes.Buffer не возвращает ошибок.
	_ = xml.EscapeText(&b, []byte(text))
	return b.String()
}

// cleanXMLText приводит произвольную строку (в том числе из OCR) к виду,
// допустимому внутри XML 1.0: чинит битый UTF-8, убирает управляющие
// символы и заменяет переводы строк пробелами, потому что каждый абзац
// документа строится отдельно. Пробелы по краям сохраняются: благодаря
// xml:space="preserve" они разделяют соседние run'ы («Код программы: » и
// значение).
func cleanXMLText(text string) string {
	text = strings.ToValidUTF8(text, "\uFFFD")
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteByte(' ')
		case r < 0x20:
			// Управляющие символы запрещены в XML 1.0.
		case r == 0xFFFE || r == 0xFFFF:
			// Не-символы XML 1.0.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
