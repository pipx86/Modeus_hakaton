package programdocx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"activity-descriptor/internal/ingestion"
)

var allowedFileName = regexp.MustCompile(`^program-[a-z0-9._-]+\.docx$`)

func sampleDraft(status string) Draft {
	return Draft{
		Key:        "mirea/09.03.01/2024/full-time",
		Status:     status,
		SourceName: "fgos-09.03.01.pdf",
		CreatedAt:  time.Date(2024, 5, 17, 9, 5, 0, 0, time.UTC),
		Warnings: []string{
			"страница 2 распознана OCR",
			"в названии есть & и <теги>",
		},
		Program: ingestion.EducationProgram{
			Title:        "Программная инженерия",
			Code:         "09.03.01",
			Organization: "МИРЭА — Российский технологический университет",
			Competencies: []ingestion.ProgramCompetency{
				{
					Code:  "УК-1",
					Title: "Способен осуществлять поиск, критический анализ и синтез информации",
					Type:  "universal",
					KSA: []ingestion.ProgramKSA{
						{Kind: "knowledge", Title: "методы поиска и анализа информации"},
						{Kind: "skill", Title: "анализировать источники"},
						{Kind: "ability", Title: "навыками системного анализа"},
					},
				},
				{
					Code:  "ПК-2",
					Title: "Способен разрабатывать программное обеспечение",
					Type:  "professional",
				},
				{
					Code:  "ОПК-3",
					Title: "Способен применять математические модели",
					Type:  "general_professional",
					KSA: []ingestion.ProgramKSA{
						{Kind: "knowledge", Title: "основы математического анализа"},
					},
				},
			},
		},
	}
}

func buildSample(t *testing.T, draft Draft) ([]byte, map[string][]byte) {
	t.Helper()
	data, err := Build(draft)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return data, readDOCXParts(t, data)
}

func readDOCXParts(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("DOCX не является валидным ZIP: %v", err)
	}
	parts := make(map[string][]byte, len(reader.File))
	for _, file := range reader.File {
		opened, openErr := file.Open()
		if openErr != nil {
			t.Fatalf("открыть %s: %v", file.Name, openErr)
		}
		content, readErr := io.ReadAll(opened)
		closeErr := opened.Close()
		if readErr != nil {
			t.Fatalf("прочитать %s: %v", file.Name, readErr)
		}
		if closeErr != nil {
			t.Fatalf("закрыть %s: %v", file.Name, closeErr)
		}
		parts[file.Name] = content
	}
	return parts
}

func assertWellFormedXML(t *testing.T, name string, data []byte) {
	t.Helper()
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		_, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatalf("%s — невалидный XML: %v", name, err)
		}
	}
}

// documentParagraphs возвращает текст каждого w:p. Это точнее, чем склейка
// всех w:t: метка и значение реквизита — два run'а одного абзаца, и в тексте
// документа они должны стоять рядом, а не на разных строках.
func documentParagraphs(t *testing.T, documentXML []byte) []string {
	t.Helper()
	decoder := xml.NewDecoder(bytes.NewReader(documentXML))
	paragraphs := make([]string, 0, 64)
	var current strings.Builder
	inParagraph := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return paragraphs
		}
		if err != nil {
			t.Fatalf("разобрать word/document.xml: %v", err)
		}
		switch node := token.(type) {
		case xml.StartElement:
			switch node.Name.Local {
			case "p":
				inParagraph = true
				current.Reset()
			case "t":
				if !inParagraph {
					continue
				}
				var text string
				if err := decoder.DecodeElement(&text, &node); err != nil {
					t.Fatalf("прочитать w:t: %v", err)
				}
				current.WriteString(text)
			}
		case xml.EndElement:
			if node.Name.Local == "p" && inParagraph {
				inParagraph = false
				paragraphs = append(paragraphs, current.String())
			}
		}
	}
}

func TestBuildProducesValidDOCX(t *testing.T) {
	data, parts := buildSample(t, sampleDraft(statusDraft))

	for _, name := range []string{
		"[Content_Types].xml",
		"_rels/.rels",
		"word/document.xml",
		"word/_rels/document.xml.rels",
		"word/styles.xml",
		"docProps/core.xml",
		"docProps/app.xml",
	} {
		if _, ok := parts[name]; !ok {
			t.Fatalf("в DOCX нет обязательной части %s", name)
		}
	}
	for name, content := range parts {
		if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".rels") {
			assertWellFormedXML(t, name, content)
		}
	}

	documentXML := parts["word/document.xml"]
	if !utf8.Valid(documentXML) {
		t.Fatal("word/document.xml не является валидным UTF-8")
	}
	if bytes.Contains(documentXML, []byte{0}) {
		t.Fatal("в word/document.xml попал NUL-байт")
	}

	text := strings.Join(documentParagraphs(t, documentXML), "\n")
	for _, want := range []string{
		"Образовательная программа",
		"Программная инженерия",
		"Код программы: 09.03.01",
		"Организация: МИРЭА — Российский технологический университет",
		"Источник: fgos-09.03.01.pdf",
		"Дата создания черновика: 17.05.2024 09:05",
		"Статус: Черновик",
		"Компетенций: 3, знаний/умений/навыков: 4, предупреждений: 2",
		"УК-1",
		"Способен осуществлять поиск, критический анализ и синтез информации",
		"Знать: методы поиска и анализа информации",
		"Уметь: анализировать источники",
		"Владеть: навыками системного анализа",
		"ПК-2",
		"ОПК-3",
		"1. страница 2 распознана OCR",
		"2. в названии есть & и <теги>",
		"Черновик. Требует проверки методистом.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("в документе нет строки %q; текст документа:\n%s", want, text)
		}
	}

	raw := string(documentXML)
	for _, want := range []string{
		`w:pgSz w:w="11906" w:h="16838"`,
		`w:pgMar w:top="1134" w:right="1134" w:bottom="1134" w:left="1134"`,
		`w:ascii="Times New Roman"`,
		`w:sz w:val="22"`,
		`w:sz w:val="32"`,
		`<w:tblBorders>`,
		`<w:tblHeader/>`,
		`<w:gridCol w:w="500"/>`,
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("в word/document.xml нет фрагмента %q", want)
		}
	}

	firstRowEnd := strings.Index(raw, "</w:tr>")
	if firstRowEnd < 0 {
		t.Fatal("в документе нет строки таблицы")
	}
	headerRow := raw[:firstRowEnd]
	for _, want := range []string{">№<", ">Код<", ">Тип<", ">Компетенция<", ">Знания и умения<", `<w:b/>`, `w:fill="D9D9D9"`} {
		if !strings.Contains(headerRow, want) {
			t.Fatalf("шапка таблицы не содержит %q", want)
		}
	}

	if len(data) == 0 {
		t.Fatal("Build вернул пустой документ")
	}
}

func TestBuildStatusSignatures(t *testing.T) {
	signatures := map[string]string{
		statusDraft:     "Черновик. Требует проверки методистом.",
		statusPublished: "Опубликовано.",
		statusRejected:  "Отклонено.",
	}
	labels := map[string]string{
		statusDraft:     "Статус: Черновик",
		statusPublished: "Статус: Опубликовано",
		statusRejected:  "Статус: Отклонено",
	}

	for status, signature := range signatures {
		t.Run(status, func(t *testing.T) {
			_, parts := buildSample(t, sampleDraft(status))
			text := strings.Join(documentParagraphs(t, parts["word/document.xml"]), "\n")
			if !strings.Contains(text, labels[status]) {
				t.Fatalf("нет подписи статуса %q в тексте:\n%s", labels[status], text)
			}
			if !strings.Contains(text, signature) {
				t.Fatalf("нет подписи %q в тексте:\n%s", signature, text)
			}
			for otherStatus, otherSignature := range signatures {
				if otherStatus == status {
					continue
				}
				if strings.Contains(text, otherSignature) {
					t.Fatalf("для статуса %s в документе есть чужая подпись %q", status, otherSignature)
				}
			}
		})
	}

	// Пустой статус трактуется как draft: старые черновики могли не иметь поля.
	_, parts := buildSample(t, sampleDraft(""))
	text := strings.Join(documentParagraphs(t, parts["word/document.xml"]), "\n")
	if !strings.Contains(text, signatures[statusDraft]) {
		t.Fatalf("пустой статус должен давать подпись черновика; текст:\n%s", text)
	}
}

func TestBuildCompetencyWithoutKSAShowsDash(t *testing.T) {
	_, parts := buildSample(t, sampleDraft(statusDraft))
	texts := documentParagraphs(t, parts["word/document.xml"])
	dashes := 0
	for _, text := range texts {
		if text == "-" {
			dashes++
		}
	}
	if dashes != 1 {
		t.Fatalf("ожидали ровно одну ячейку «-» у компетенции без KSA, получили %d; тексты: %v", dashes, texts)
	}
}

func TestBuildEmptyProgramIsReadable(t *testing.T) {
	data, parts := buildSample(t, Draft{})
	if len(data) == 0 {
		t.Fatal("Build вернул пустой документ")
	}
	text := strings.Join(documentParagraphs(t, parts["word/document.xml"]), "\n")
	for _, want := range []string{
		"Образовательная программа",
		"Без названия",
		"Код программы: —",
		"Компетенций: 0, знаний/умений/навыков: 0, предупреждений: 0",
		"Компетенции не найдены.",
		"Черновик. Требует проверки методистом.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("в документе нет строки %q; текст:\n%s", want, text)
		}
	}
}

func TestBuildEscapesUnsafeCharacters(t *testing.T) {
	draft := sampleDraft(statusDraft)
	draft.Program.Title = "Разработка ПО & <сопровождение> \"полного цикла\"\x00"
	draft.Program.Competencies[0].Title = "A & B < C > D"
	draft.Warnings = []string{"контроль\x01символа и <XML>"}

	_, parts := buildSample(t, draft)
	documentXML := parts["word/document.xml"]
	assertWellFormedXML(t, "word/document.xml", documentXML)
	if bytes.Contains(documentXML, []byte{0}) || bytes.Contains(documentXML, []byte{1}) {
		t.Fatal("управляющие символы попали в XML")
	}

	text := strings.Join(documentParagraphs(t, documentXML), "\n")
	for _, want := range []string{
		`Разработка ПО & <сопровождение> "полного цикла"`,
		"A & B < C > D",
		"контрольсимвола и <XML>",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("после XML-экранирования потерялся текст %q; текст:\n%s", want, text)
		}
	}
}

func TestFileName(t *testing.T) {
	longCode := strings.Repeat("A", 200)
	tests := []struct {
		name string
		code string
		want string
	}{
		{
			name: "кириллица, слэш и пробел",
			code: "09.03.01/Программа ИТ",
			want: "program-09.03.01" + strings.Repeat("_", 13) + ".docx",
		},
		{
			name: "пробелы и слэш",
			code: "Program 2024/2025",
			want: "program-program_2024_2025.docx",
		},
		{
			name: "кириллица с дефисом",
			code: "ПК-1",
			want: "program-__-1.docx",
		},
		{
			name: "пустой код",
			code: "",
			want: "program-draft.docx",
		},
		{
			name: "код из пробелов",
			code: "   ",
			want: "program-draft.docx",
		},
		{
			name: "кавычки, обратный слэш и перевод строки",
			code: "a\"b/c\\d\ne",
			want: "program-a_b_c_d_e.docx",
		},
		{
			name: "длинный код",
			code: longCode,
			want: "program-" + strings.Repeat("a", 67) + ".docx",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := FileName(test.code)
			if got != test.want {
				t.Fatalf("FileName(%q) = %q, ожидали %q", test.code, got, test.want)
			}
			if !allowedFileName.MatchString(got) {
				t.Fatalf("имя %q не соответствует безопасному шаблону", got)
			}
			if len(got) > maxFileNameLen {
				t.Fatalf("имя %q длиннее %d символов: %d", got, maxFileNameLen, len(got))
			}
		})
	}
}
