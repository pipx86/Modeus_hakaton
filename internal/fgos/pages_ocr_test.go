package fgos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

const ocrTestLongText = "BT /F1 12 Tf 72 720 Td (FGOS text layer page with enough characters) Tj ET"

// buildMultiPagePDF собирает PDF с одной или несколькими страницами. Пустой
// content — страница без текстового слоя (скан).
func buildMultiPagePDF(t *testing.T, contents ...string) []byte {
	t.Helper()
	if len(contents) == 0 {
		contents = []string{""}
	}
	total := len(contents)
	fontNumber := 3 + 2*total
	kids := make([]string, total)
	for index := 0; index < total; index++ {
		kids[index] = fmt.Sprintf("%d 0 R", 3+2*index)
	}
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), total),
	}
	for index, content := range contents {
		pageNumber := 3 + 2*index
		contentNumber := pageNumber + 1
		objects = append(objects,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents %d 0 R /Resources << /Font << /F1 %d 0 R >> >> >>",
				contentNumber, fontNumber),
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	}
	objects = append(objects, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

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

// fakeOCRExtractor — OCRExtractor без tesseract: возвращает заранее заданные
// страницы и запоминает пути временных файлов.
type fakeOCRExtractor struct {
	pages       []Page
	err         error
	pdfCalls    int
	imageCalls  int
	lastPDF     string
	lastImage   string
	pdfDataSeen []byte
}

func (f *fakeOCRExtractor) PagesFromPDF(_ context.Context, path string) ([]Page, error) {
	f.pdfCalls++
	f.lastPDF = path
	if data, err := os.ReadFile(path); err == nil {
		f.pdfDataSeen = data
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.pages, nil
}

func (f *fakeOCRExtractor) RecognizeImage(_ context.Context, imagePath string) ([]Page, error) {
	f.imageCalls++
	f.lastImage = imagePath
	if f.err != nil {
		return nil, f.err
	}
	return f.pages, nil
}

func TestExtractPagesWithOCRRecognizesScanPDF(t *testing.T) {
	recognizedText := "УК-1. Способен осуществлять поиск и критический анализ информации."
	ocr := &fakeOCRExtractor{pages: []Page{{Number: 1, Text: recognizedText, OCR: true}}}
	pages, err := ExtractPagesWithOCR(context.Background(), "scan.pdf", buildPDF(t, ""), ocr)
	if err != nil {
		t.Fatalf("ExtractPagesWithOCR: %v", err)
	}
	if len(pages) != 1 || !pages[0].OCR || pages[0].Text != recognizedText {
		t.Fatalf("страницы: %+v", pages)
	}
	if ocr.pdfCalls != 1 {
		t.Fatalf("OCR должен вызываться один раз: %d", ocr.pdfCalls)
	}
	if !strings.HasSuffix(ocr.lastPDF, ".pdf") {
		t.Fatalf("OCR получил путь без .pdf: %q", ocr.lastPDF)
	}
	if len(ocr.pdfDataSeen) == 0 {
		t.Fatal("виртуальный PDF не был записан во временный файл")
	}
	if _, statErr := os.Stat(ocr.lastPDF); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("временный PDF не удалён: %v", statErr)
	}
}

func TestExtractPagesWithOCRKeepsTextLayerPDF(t *testing.T) {
	ocr := &fakeOCRExtractor{pages: []Page{{Number: 1, Text: "не должен использоваться", OCR: true}}}
	pages, err := ExtractPagesWithOCR(context.Background(), "fgos.pdf", buildPDF(t, ocrTestLongText), ocr)
	if err != nil {
		t.Fatalf("ExtractPagesWithOCR: %v", err)
	}
	if ocr.pdfCalls != 0 {
		t.Fatalf("PDF с текстовым слоем не должен уходить в OCR: %d", ocr.pdfCalls)
	}
	if len(pages) != 1 || pages[0].OCR || !strings.Contains(pages[0].Text, "FGOS") {
		t.Fatalf("страницы: %+v", pages)
	}
}

func TestExtractPagesWithOCRRecognizesMixedDocument(t *testing.T) {
	ocr := &fakeOCRExtractor{pages: []Page{
		{Number: 1, Text: "Первая страница с текстовым слоем" + strings.Repeat(" ", 20)},
		{Number: 2, Text: "ОПК-2. Способен разрабатывать программное обеспечение.", OCR: true},
	}}
	pages, err := ExtractPagesWithOCR(context.Background(), "mixed.pdf", buildMultiPagePDF(t, ocrTestLongText, ""), ocr)
	if err != nil {
		t.Fatalf("ExtractPagesWithOCR: %v", err)
	}
	if ocr.pdfCalls != 1 {
		t.Fatalf("смешанный документ должен уходить в OCR: %d", ocr.pdfCalls)
	}
	if len(pages) != 2 || pages[0].OCR || !pages[1].OCR {
		t.Fatalf("флаги OCR по страницам: %+v", pages)
	}
}

func TestExtractPagesWithOCRPropagatesOCRDisabled(t *testing.T) {
	disabled := errors.New("OCR выключен")
	ocr := &fakeOCRExtractor{err: disabled}
	_, err := ExtractPagesWithOCR(context.Background(), "scan.pdf", buildPDF(t, ""), ocr)
	if !errors.Is(err, disabled) {
		t.Fatalf("ошибка OCR не проброшена: %v", err)
	}
}

func TestExtractPagesWithOCRImage(t *testing.T) {
	ocr := &fakeOCRExtractor{pages: []Page{{Number: 1, Text: "УК-1. Способен осуществлять поиск информации.", OCR: true}}}
	pages, err := ExtractPagesWithOCR(context.Background(), "scan.png", []byte("fake png"), ocr)
	if err != nil {
		t.Fatalf("ExtractPagesWithOCR: %v", err)
	}
	if ocr.imageCalls != 1 || len(pages) != 1 || !pages[0].OCR {
		t.Fatalf("изображение: calls=%d pages=%+v", ocr.imageCalls, pages)
	}
	if !strings.HasSuffix(ocr.lastImage, ".png") {
		t.Fatalf("OCR получил путь без .png: %q", ocr.lastImage)
	}
	if _, statErr := os.Stat(ocr.lastImage); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("временное изображение не удалено: %v", statErr)
	}
}

func TestExtractPagesImageWithoutOCRIsUnsupported(t *testing.T) {
	_, err := ExtractPagesWithOCR(context.Background(), "scan.jpg", []byte("fake jpg"), nil)
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("ожидали ErrUnsupportedFormat, получили %v", err)
	}
	if !strings.Contains(err.Error(), "требует OCR") {
		t.Fatalf("текст ошибки: %v", err)
	}
}

func TestExtractAddsWarningForOCRPage(t *testing.T) {
	ocr := &fakeOCRExtractor{pages: []Page{{
		Number: 1,
		Text:   "УК-1. Способен осуществлять поиск и критический анализ информации.",
		OCR:    true,
	}}}
	pages, err := ExtractPagesWithOCR(context.Background(), "scan.pdf", buildPDF(t, ""), ocr)
	if err != nil {
		t.Fatalf("ExtractPagesWithOCR: %v", err)
	}
	client := &fakeLLM{answers: []string{
		answerFor("УК-1", "Способен осуществлять поиск и критический анализ информации", "УК",
			"знание", "методы системного анализа", 1, quoteOne),
	}}
	program, warnings, err := NewExtractor(client).Extract(context.Background(), pages)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(program.Competencies) != 1 {
		t.Fatalf("компетенции: %+v", program.Competencies)
	}
	want := OCRPageWarning(1)
	for _, warning := range warnings {
		if warning == want {
			return
		}
	}
	t.Fatalf("нет предупреждения %q в %v", want, warnings)
}
