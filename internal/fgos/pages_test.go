package fgos

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const docxDocumentXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>УК-1. Первая страница: способен анализировать информацию.</w:t></w:r></w:p>
    <w:p><w:r><w:br w:type="page"/></w:r></w:p>
    <w:p><w:r><w:t>ОПК-2. Вторая страница: способен разрабатывать программное обеспечение.</w:t></w:r></w:p>
    <w:p><w:r><w:lastRenderedPageBreak/><w:t>ПК-3. Третья страница: способен проектировать базы данных.</w:t></w:r></w:p>
  </w:body>
</w:document>`

func buildDOCX(t *testing.T, documentXML string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatalf("zip.Create: %v", err)
	}
	if _, err = file.Write([]byte(documentXML)); err != nil {
		t.Fatalf("zip.Write: %v", err)
	}
	if err = writer.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buffer.Bytes()
}

// buildPDF собирает минимальный валидный PDF с одной страницей и корректной
// таблицей xref: библиотеке нужны настоящие смещения объектов.
func buildPDF(t *testing.T, contentStream string) []byte {
	t.Helper()
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(contentStream), contentStream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var builder strings.Builder
	builder.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = builder.Len()
		fmt.Fprintf(&builder, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xrefOffset := builder.Len()
	fmt.Fprintf(&builder, "xref\n0 %d\n", len(objects)+1)
	builder.WriteString("0000000000 65535 f \n")
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&builder, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&builder, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xrefOffset)
	return []byte(builder.String())
}

func TestExtractPagesDOCXSplitsByPageBreaks(t *testing.T) {
	pages, err := ExtractPages("fgos.docx", buildDOCX(t, docxDocumentXML))
	if err != nil {
		t.Fatalf("ExtractPages: %v", err)
	}
	if len(pages) != 3 {
		t.Fatalf("ожидали 3 страницы, получили %d: %+v", len(pages), pages)
	}
	if pages[0].Number != 1 || !strings.Contains(pages[0].Text, "Первая страница") {
		t.Fatalf("страница 1: %+v", pages[0])
	}
	if pages[1].Number != 2 || !strings.Contains(pages[1].Text, "Вторая страница") {
		t.Fatalf("страница 2: %+v", pages[1])
	}
	if pages[2].Number != 3 || !strings.Contains(pages[2].Text, "Третья страница") {
		t.Fatalf("страница 3: %+v", pages[2])
	}
	if strings.Contains(pages[0].Text, "Вторая страница") {
		t.Fatal("разрыв страницы не должен попадать в первую страницу")
	}
}

func TestExtractPagesDOCXWithoutPageBreaks(t *testing.T) {
	document := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body><w:p><w:r><w:t>Единственная страница с текстом про компетенции.</w:t></w:r></w:p></w:body></w:document>`
	pages, err := ExtractPages("fgos.docx", buildDOCX(t, document))
	if err != nil {
		t.Fatalf("ExtractPages: %v", err)
	}
	if len(pages) != 1 || pages[0].Number != 1 {
		t.Fatalf("без разрывов должна быть одна страница: %+v", pages)
	}
	if !strings.Contains(pages[0].Text, "Единственная страница") {
		t.Fatalf("текст страницы: %q", pages[0].Text)
	}
}

func TestExtractPagesDOCXWithoutText(t *testing.T) {
	document := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p/></w:body></w:document>`
	_, err := ExtractPages("fgos.docx", buildDOCX(t, document))
	if !errors.Is(err, ErrNoTextLayer) {
		t.Fatalf("ожидали ErrNoTextLayer, получили %v", err)
	}
	if !strings.Contains(err.Error(), "нет текстового слоя, нужен OCR") {
		t.Fatalf("текст ошибки: %v", err)
	}
}

func TestExtractPagesPDFWithoutTextLayer(t *testing.T) {
	_, err := ExtractPages("scan.pdf", buildPDF(t, ""))
	if !errors.Is(err, ErrNoTextLayer) {
		t.Fatalf("ожидали ErrNoTextLayer, получили %v", err)
	}
	if !strings.Contains(err.Error(), "нет текстового слоя, нужен OCR") {
		t.Fatalf("текст ошибки: %v", err)
	}
}

func TestExtractPagesPDFPerPage(t *testing.T) {
	stream := "BT /F1 12 Tf 72 720 Td (FGOS competence page one) Tj ET"
	pages, err := ExtractPages("fgos.pdf", buildPDF(t, stream))
	if err != nil {
		t.Fatalf("ExtractPages: %v", err)
	}
	if len(pages) != 1 || pages[0].Number != 1 {
		t.Fatalf("ожидали одну страницу: %+v", pages)
	}
	if !strings.Contains(pages[0].Text, "FGOS") {
		t.Fatalf("текст PDF: %q", pages[0].Text)
	}
}

func TestExtractPagesRejectsUnsupportedFormat(t *testing.T) {
	_, err := ExtractPages("fgos.txt", []byte("просто текст"))
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("ожидали ErrUnsupportedFormat, получили %v", err)
	}
}

func TestExtractPagesRejectsBrokenDocument(t *testing.T) {
	_, err := ExtractPages("broken.pdf", []byte("%PDF-1.7 not really a pdf"))
	if !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("ожидали ErrInvalidDocument, получили %v", err)
	}
}

func TestExtractPagesFileReadsFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fgos.docx")
	if err := os.WriteFile(path, buildDOCX(t, docxDocumentXML), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	pages, err := ExtractPagesFile(path)
	if err != nil {
		t.Fatalf("ExtractPagesFile: %v", err)
	}
	if len(pages) != 3 {
		t.Fatalf("страницы: %d", len(pages))
	}
	viaMethod, err := NewExtractor(&fakeLLM{}).ExtractPagesFile(path)
	if err != nil || len(viaMethod) != 3 {
		t.Fatalf("метод ExtractPagesFile: pages=%d err=%v", len(viaMethod), err)
	}
}
