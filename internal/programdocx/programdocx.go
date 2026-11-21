// Package programdocx собирает читабельный DOCX для разобранной
// образовательной программы: реквизиты черновика, сводку, таблицу
// компетенций с KSA, предупреждения импорта и подпись по статусу.
//
// Пакет намеренно не зависит от API и store: на вход подаётся Draft, на
// выходе — готовые байты .docx. DOCX собирается минимальным OOXML-пакетом
// (ZIP + word/document.xml + styles.xml), поэтому не тянет внешних
// зависимостей и полностью контролирует A4, поля, шрифт и границы таблицы.
package programdocx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"time"

	"activity-descriptor/internal/ingestion"
)

// ContentType — MIME-тип DOCX, который API отдаёт в Content-Type.
const ContentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

const (
	filePrefix = "program-"
	fileSuffix = ".docx"
	// maxFileNameLen — предел длины имени файла целиком, включая
	// program- и .docx. Код внутри сокращается до 67 символов.
	maxFileNameLen = 80

	missingValue = "—"
)

// Draft — данные черновика, необходимые для документа. Program хранится в
// том виде, в котором его вернул импорт.
type Draft struct {
	Key        string
	Status     string
	SourceName string
	CreatedAt  time.Time
	Warnings   []string
	Program    ingestion.EducationProgram
}

// FileName возвращает безопасное имя вложения:
// program-<код>.docx, где код состоит только из [a-z0-9._-], а все
// остальные символы заменены на "_". Длина имени не превышает 80 символов.
func FileName(code string) string {
	return filePrefix + safeCode(code) + fileSuffix
}

// safeCode нормализует код программы для имени файла. Перевод в нижний
// регистр выполняется только для ASCII: кириллица и любые другие символы
// становятся "_", как и требуется для безопасного ASCII-имени.
func safeCode(code string) string {
	maxCodeLen := maxFileNameLen - len(filePrefix) - len(fileSuffix)

	var b strings.Builder
	for _, r := range strings.TrimSpace(code) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}

	safe := strings.TrimRight(b.String(), ".")
	if len(safe) > maxCodeLen {
		safe = strings.TrimRight(safe[:maxCodeLen], ".")
	}
	if safe == "" {
		return "draft"
	}
	return safe
}

// Build собирает DOCX и возвращает его байты.
func Build(draft Draft) ([]byte, error) {
	documentXML := buildDocumentXML(draft)
	title := strings.TrimSpace(draft.Program.Title)
	if title == "" {
		title = "Образовательная программа"
	}
	created := draft.CreatedAt.UTC()
	if created.IsZero() {
		created = time.Unix(0, 0).UTC()
	}
	timestamp := created.Format(time.RFC3339)

	parts := []docxPart{
		{name: "[Content_Types].xml", data: []byte(contentTypesXML)},
		{name: "_rels/.rels", data: []byte(rootRelationshipsXML)},
		{name: "word/document.xml", data: []byte(documentXML)},
		{name: "word/_rels/document.xml.rels", data: []byte(documentRelationshipsXML)},
		{name: "word/styles.xml", data: []byte(stylesXML)},
		{name: "docProps/core.xml", data: []byte(corePropertiesXML(title, timestamp))},
		{name: "docProps/app.xml", data: []byte(appPropertiesXML)},
	}
	return writeDOCX(parts)
}

type docxPart struct {
	name string
	data []byte
}

func writeDOCX(parts []docxPart) ([]byte, error) {
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	for _, part := range parts {
		writer, err := archive.Create(part.name)
		if err != nil {
			_ = archive.Close()
			return nil, fmt.Errorf("programdocx: создать часть %s: %w", part.name, err)
		}
		if _, err = writer.Write(part.data); err != nil {
			_ = archive.Close()
			return nil, fmt.Errorf("programdocx: записать часть %s: %w", part.name, err)
		}
	}
	if err := archive.Close(); err != nil {
		return nil, fmt.Errorf("programdocx: закрыть DOCX: %w", err)
	}
	return buf.Bytes(), nil
}

func buildDocumentXML(draft Draft) string {
	program := draft.Program
	status := normalizeStatus(draft.Status)
	code := strings.TrimSpace(program.Code)
	if code == "" {
		code = strings.TrimSpace(draft.Key)
	}

	var b strings.Builder
	b.WriteString(xmlDeclaration)
	b.WriteString(`<w:document xmlns:w="` + namespaceWord + `">`)
	b.WriteString("<w:body>")

	b.WriteString(renderParagraph(
		paragraphStyle{align: "center", spaceAfter: 120},
		runStyle{text: "Образовательная программа", bold: true, size: titleFontSize},
	))
	b.WriteString(renderParagraph(
		paragraphStyle{align: "center", spaceAfter: 240},
		runStyle{text: textOr(program.Title, "Без названия"), bold: true, size: programTitleFontSize},
	))

	b.WriteString(sectionHeading("Реквизиты"))
	b.WriteString(labelValue("Код программы: ", valueOrDash(code)))
	if organization := strings.TrimSpace(program.Organization); organization != "" {
		b.WriteString(labelValue("Организация: ", organization))
	}
	b.WriteString(labelValue("Источник: ", valueOrDash(draft.SourceName)))
	b.WriteString(labelValue("Дата создания черновика: ", formatDraftDate(draft.CreatedAt)))
	b.WriteString(labelValue("Статус: ", statusLabel(status)))

	b.WriteString(renderParagraph(
		paragraphStyle{spaceBefore: 120, spaceAfter: 120},
		runStyle{text: summaryLine(program, draft.Warnings)},
	))

	b.WriteString(sectionHeading("Компетенции"))
	if len(program.Competencies) == 0 {
		b.WriteString(renderParagraph(
			paragraphStyle{spaceAfter: 120},
			runStyle{text: "Компетенции не найдены."},
		))
	} else {
		b.WriteString(renderTable(program.Competencies))
	}

	if len(draft.Warnings) > 0 {
		b.WriteString(sectionHeading("Предупреждения"))
		for i, warning := range draft.Warnings {
			text := strings.TrimSpace(cleanXMLText(warning))
			if text == "" {
				text = missingValue
			}
			b.WriteString(renderParagraph(
				paragraphStyle{spaceAfter: 60, indentLeft: 360, indentHanging: 360},
				runStyle{text: fmt.Sprintf("%d. %s", i+1, text)},
			))
		}
	}

	b.WriteString(renderParagraph(
		paragraphStyle{align: "center", spaceBefore: 360, spaceAfter: 120},
		runStyle{text: statusSignature(status), italic: true},
	))

	b.WriteString(sectionProperties)
	b.WriteString("</w:body></w:document>")
	return b.String()
}

func sectionHeading(text string) string {
	return renderParagraph(
		paragraphStyle{spaceBefore: 240, spaceAfter: 120, keepNext: true},
		runStyle{text: text, bold: true, size: sectionFontSize},
	)
}

func labelValue(label, value string) string {
	return renderParagraph(
		paragraphStyle{spaceAfter: 60},
		runStyle{text: label, bold: true},
		runStyle{text: value},
	)
}

func summaryLine(program ingestion.EducationProgram, warnings []string) string {
	return fmt.Sprintf("Компетенций: %d, знаний/умений/навыков: %d, предупреждений: %d",
		len(program.Competencies), countKSA(program), len(warnings))
}

func countKSA(program ingestion.EducationProgram) int {
	count := 0
	for _, competency := range program.Competencies {
		count += len(competency.KSA)
	}
	return count
}

const (
	statusDraft     = "draft"
	statusPublished = "published"
	statusRejected  = "rejected"
)

func normalizeStatus(status string) string {
	switch normalized := strings.ToLower(strings.TrimSpace(status)); normalized {
	case "", statusDraft:
		return statusDraft
	case statusPublished:
		return statusPublished
	case statusRejected:
		return statusRejected
	default:
		return normalized
	}
}

func statusLabel(status string) string {
	switch normalized := normalizeStatus(status); normalized {
	case statusDraft:
		return "Черновик"
	case statusPublished:
		return "Опубликовано"
	case statusRejected:
		return "Отклонено"
	default:
		return normalized
	}
}

func statusSignature(status string) string {
	switch normalizeStatus(status) {
	case statusPublished:
		return "Опубликовано."
	case statusRejected:
		return "Отклонено."
	default:
		return "Черновик. Требует проверки методистом."
	}
}

// competencyTypeLabel переводит канонический или устаревший type в короткую
// пометку для человека. Неизвестный тип показывается как «иное», пустой — «—».
func competencyTypeLabel(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.Join(strings.Fields(normalized), "_")

	switch normalized {
	case "":
		return missingValue
	case "universal", "ук", "универсальная", "универсальные", "универсальная_компетенция":
		return "УК"
	case "general_professional", "generalprofessional", "опк", "общепрофессиональная", "общепрофессиональные", "общепрофессиональная_компетенция":
		return "ОПК"
	case "professional", "пк", "профессиональная", "профессиональные", "профессиональная_компетенция":
		return "ПК"
	default:
		return "иное"
	}
}

// ksaLines возвращает строки для ячейки «Знания и умения». Порядок
// канонический: Знать, Уметь, Владеть; незнакомые виды KSA идут после них.
func ksaLines(ksa []ingestion.ProgramKSA) []string {
	if len(ksa) == 0 {
		return []string{"-"}
	}

	lines := make([]string, 0, len(ksa))
	used := make([]bool, len(ksa))
	for _, kind := range []string{"knowledge", "skill", "ability"} {
		for i, item := range ksa {
			if used[i] || normalizeKSAKind(item.Kind) != kind {
				continue
			}
			used[i] = true
			if line := ksaLine(item); line != "" {
				lines = append(lines, line)
			}
		}
	}
	for i, item := range ksa {
		if used[i] {
			continue
		}
		if line := ksaLine(item); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return []string{"-"}
	}
	return lines
}

func ksaLine(item ingestion.ProgramKSA) string {
	title := strings.TrimSpace(cleanXMLText(item.Title))
	if title == "" {
		return ""
	}
	return ksaKindLabel(item.Kind) + title
}

func ksaKindLabel(kind string) string {
	switch normalizeKSAKind(kind) {
	case "knowledge":
		return "Знать: "
	case "skill":
		return "Уметь: "
	case "ability":
		return "Владеть: "
	default:
		cleaned := strings.TrimSpace(cleanXMLText(kind))
		if cleaned == "" {
			return "KSA: "
		}
		return cleaned + ": "
	}
}

func normalizeKSAKind(kind string) string {
	return strings.ToLower(strings.TrimSpace(kind))
}

func formatDraftDate(created time.Time) string {
	if created.IsZero() {
		return missingValue
	}
	return created.Format("02.01.2006 15:04")
}

func textOr(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func valueOrDash(value string) string {
	return textOr(value, missingValue)
}

const tableWidth = 9638

// tableColumnWidths — ширины колонок в twips. В сумме дают tableWidth:
// 500 + 1100 + 700 + 3338 + 4000 = 9638, то есть всю ширину A4 минус поля 2 см.
var tableColumnWidths = [5]int{500, 1100, 700, 3338, 4000}

func renderTable(competencies []ingestion.ProgramCompetency) string {
	var b strings.Builder
	b.WriteString("<w:tbl><w:tblPr>")
	fmt.Fprintf(&b, `<w:tblW w:w="%d" w:type="dxa"/>`, tableWidth)
	b.WriteString(`<w:tblLayout w:type="fixed"/>`)
	b.WriteString("<w:tblBorders>")
	for _, side := range []string{"top", "left", "bottom", "right", "insideH", "insideV"} {
		fmt.Fprintf(&b, `<w:%s w:val="single" w:sz="4" w:space="0" w:color="000000"/>`, side)
	}
	b.WriteString("</w:tblBorders>")
	b.WriteString(`<w:tblCellMar><w:top w:w="60" w:type="dxa"/><w:left w:w="80" w:type="dxa"/><w:bottom w:w="60" w:type="dxa"/><w:right w:w="80" w:type="dxa"/></w:tblCellMar>`)
	b.WriteString("</w:tblPr><w:tblGrid>")
	for _, width := range tableColumnWidths {
		fmt.Fprintf(&b, `<w:gridCol w:w="%d"/>`, width)
	}
	b.WriteString("</w:tblGrid>")

	b.WriteString("<w:tr><w:trPr><w:tblHeader/></w:trPr>")
	for i, header := range []string{"№", "Код", "Тип", "Компетенция", "Знания и умения"} {
		b.WriteString(renderTableCell(tableColumnWidths[i], true, []string{header}))
	}
	b.WriteString("</w:tr>")

	for i, competency := range competencies {
		b.WriteString("<w:tr>")
		b.WriteString(renderTableCell(tableColumnWidths[0], false, []string{fmt.Sprintf("%d", i+1)}))
		b.WriteString(renderTableCell(tableColumnWidths[1], false, []string{valueOrDash(competency.Code)}))
		b.WriteString(renderTableCell(tableColumnWidths[2], false, []string{competencyTypeLabel(competency.Type)}))
		b.WriteString(renderTableCell(tableColumnWidths[3], false, []string{valueOrDash(competency.Title)}))
		b.WriteString(renderTableCell(tableColumnWidths[4], false, ksaLines(competency.KSA)))
		b.WriteString("</w:tr>")
	}
	b.WriteString("</w:tbl>")
	return b.String()
}

func renderTableCell(width int, header bool, lines []string) string {
	var b strings.Builder
	b.WriteString("<w:tc><w:tcPr>")
	fmt.Fprintf(&b, `<w:tcW w:w="%d" w:type="dxa"/>`, width)
	if header {
		b.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="D9D9D9"/>`)
		b.WriteString(`<w:vAlign w:val="center"/>`)
	} else {
		b.WriteString(`<w:vAlign w:val="top"/>`)
	}
	b.WriteString("</w:tcPr>")
	for _, line := range lines {
		b.WriteString(renderParagraph(
			paragraphStyle{spaceAfter: -1, line: 240},
			runStyle{text: line, bold: header},
		))
	}
	b.WriteString("</w:tc>")
	return b.String()
}
