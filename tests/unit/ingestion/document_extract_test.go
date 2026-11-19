package ingestion_test

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"activity-descriptor/internal/ingestion"
)

func makeDOCX(t *testing.T, body string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	file, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	document := `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>` + body + `</w:body>
</w:document>`
	if _, err = file.Write([]byte(document)); err != nil {
		t.Fatalf("write docx: %v", err)
	}
	if err = archive.Close(); err != nil {
		t.Fatalf("close docx: %v", err)
	}
	return buffer.Bytes()
}

func TestExtractDocumentTextPlain(t *testing.T) {
	text, err := ingestion.ExtractDocumentText("role.md", []byte("# Роль\nТекст"))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !strings.Contains(text, "Роль") {
		t.Fatalf("text = %q", text)
	}
}

func TestExtractDocumentTextDOCX(t *testing.T) {
	data := makeDOCX(t, `<w:p><w:r><w:t>Первая строка</w:t></w:r></w:p><w:p><w:r><w:t>Вторая строка</w:t></w:r></w:p>`)
	text, err := ingestion.ExtractDocumentText("standard.docx", data)
	if err != nil {
		t.Fatalf("extract docx: %v", err)
	}
	if !strings.Contains(text, "Первая строка") || !strings.Contains(text, "Вторая строка") {
		t.Fatalf("text = %q", text)
	}
	if !strings.Contains(text, "\n") {
		t.Fatalf("абзацы не разделены переводом строки: %q", text)
	}
}

func TestExtractDocumentTextRejectsUnsupported(t *testing.T) {
	if _, err := ingestion.ExtractDocumentText("image.png", []byte("png")); err == nil {
		t.Fatal("ожидали ошибку для неподдерживаемого формата")
	}
}

func TestExtractDocumentTextRejectsEmpty(t *testing.T) {
	if _, err := ingestion.ExtractDocumentText("empty.txt", nil); err == nil {
		t.Fatal("ожидали ошибку для пустого файла")
	}
}

func TestExtractDocumentTextRejectsEmptyContent(t *testing.T) {
	if _, err := ingestion.ExtractDocumentText("empty.txt", []byte("   \n  ")); err == nil {
		t.Fatal("ожидали ошибку для файла без текста")
	}
}
