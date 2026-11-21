package ocr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"activity-descriptor/internal/fgos"
)

const pdfLongText = "BT /F1 12 Tf 72 720 Td (FGOS competence page with a real text layer) Tj ET"

// buildPDF собирает минимальный валидный PDF с одной или несколькими
// страницами и корректной таблицей xref: библиотеке нужны настоящие смещения
// объектов. Пустой content означает страницу без текстового слоя.
func buildPDF(t *testing.T, contents ...string) []byte {
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

func writePDF(t *testing.T, contents ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "document.pdf")
	if err := os.WriteFile(path, buildPDF(t, contents...), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// fakeEngine — Engine без внешнего tesseract. texts и errs ключуются путём к
// изображению, block эмулирует зависший tesseract до отмены контекста.
type fakeEngine struct {
	mu    sync.Mutex
	texts map[string]string
	errs  map[string]error
	calls []string
	langs []string
	block bool
}

func (f *fakeEngine) Recognize(ctx context.Context, imagePath, lang string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, imagePath)
	f.langs = append(f.langs, lang)
	text, hasText := f.texts[imagePath]
	recognizeErr := f.errs[imagePath]
	block := f.block
	f.mu.Unlock()

	if block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if recognizeErr != nil {
		return "", recognizeErr
	}
	if hasText {
		return text, nil
	}
	return "", ErrNoText
}

func (f *fakeEngine) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeRenderer — Renderer без pdftoppm: images ключуются диапазоном "first-last".
type fakeRenderer struct {
	mu        sync.Mutex
	images    map[string][]string
	renderErr error
	calls     []string
	cleanups  int
}

func (f *fakeRenderer) Render(_ context.Context, _ string, firstPage, lastPage int) ([]string, func(), error) {
	f.mu.Lock()
	key := fmt.Sprintf("%d-%d", firstPage, lastPage)
	f.calls = append(f.calls, key)
	images := append([]string(nil), f.images[key]...)
	renderErr := f.renderErr
	f.mu.Unlock()
	if renderErr != nil {
		return nil, nil, renderErr
	}
	cleanup := func() {
		f.mu.Lock()
		f.cleanups++
		f.mu.Unlock()
	}
	return images, cleanup, nil
}

func (f *fakeRenderer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func TestPagesFromPDFRecognizesOnlyPageWithoutText(t *testing.T) {
	t.Setenv("FGOS_PDFTOTEXT", "off") // тест проверяет встроенный извлекатель, не poppler
	renderer := &fakeRenderer{images: map[string][]string{"2-2": {"page-2.png"}}}
	engine := &fakeEngine{texts: map[string]string{
		"page-2.png": "ОПК-2. Способен разрабатывать программное обеспечение.",
	}}
	document := &Document{
		Enabled:  true,
		Lang:     "rus+eng",
		MaxPages: 20,
		Timeout:  2 * time.Second,
		Engine:   engine,
		Renderer: renderer,
	}
	pages, err := document.PagesFromPDF(context.Background(), writePDF(t, pdfLongText, ""))
	if err != nil {
		t.Fatalf("PagesFromPDF: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("страницы: %+v", pages)
	}
	if pages[0].Number != 1 || pages[0].OCR || !strings.Contains(pages[0].Text, "FGOS") {
		t.Fatalf("страница с текстовым слоем: %+v", pages[0])
	}
	if pages[1].Number != 2 || !pages[1].OCR || !strings.Contains(pages[1].Text, "ОПК-2") {
		t.Fatalf("распознанная страница: %+v", pages[1])
	}
	if renderer.callCount() != 1 || len(renderer.calls) != 1 || renderer.calls[0] != "2-2" {
		t.Fatalf("рендерились не те страницы: %v", renderer.calls)
	}
	if renderer.cleanups != 1 {
		t.Fatalf("временные файлы не убраны: cleanups=%d", renderer.cleanups)
	}
	if engine.count() != 1 || engine.langs[0] != "rus+eng" {
		t.Fatalf("вызовы OCR: %v %v", engine.calls, engine.langs)
	}
}

func TestPagesFromPDFRecognizesAllPagesWithoutTextLayer(t *testing.T) {
	renderer := &fakeRenderer{images: map[string][]string{"1-2": {"page-1.png", "page-2.png"}}}
	engine := &fakeEngine{texts: map[string]string{
		"page-1.png": "УК-1. Способен осуществлять поиск информации.",
		"page-2.png": "ОПК-2. Способен разрабатывать программное обеспечение.",
	}}
	document := &Document{Enabled: true, MaxPages: 20, Timeout: 2 * time.Second, Engine: engine, Renderer: renderer}
	pages, err := document.PagesFromPDF(context.Background(), writePDF(t, "", ""))
	if err != nil {
		t.Fatalf("PagesFromPDF: %v", err)
	}
	if len(pages) != 2 || !pages[0].OCR || !pages[1].OCR {
		t.Fatalf("скан должен распознаваться целиком: %+v", pages)
	}
	if renderer.calls[0] != "1-2" {
		t.Fatalf("диапазон рендеринга: %v", renderer.calls)
	}
}

func TestPagesFromPDFKeepsCompleteTextLayerWithoutOCR(t *testing.T) {
	document := &Document{Enabled: false, MaxPages: 20, Timeout: time.Second}
	pages, err := document.PagesFromPDF(context.Background(), writePDF(t, pdfLongText))
	if err != nil {
		t.Fatalf("PagesFromPDF: %v", err)
	}
	if len(pages) != 1 || pages[0].OCR || !strings.Contains(pages[0].Text, "FGOS") {
		t.Fatalf("текстовый слой должен сохраниться без OCR: %+v", pages)
	}
}

func TestPagesFromPDFSkipsBlankPageWhenOCRReturnsNoText(t *testing.T) {
	renderer := &fakeRenderer{images: map[string][]string{"2-2": {"page-2.png"}}}
	engine := &fakeEngine{errs: map[string]error{"page-2.png": ErrNoText}}
	document := &Document{Enabled: true, MaxPages: 20, Timeout: time.Second, Engine: engine, Renderer: renderer}
	pages, err := document.PagesFromPDF(context.Background(), writePDF(t, pdfLongText, ""))
	if err != nil {
		t.Fatalf("пустая страница не должна валить документ: %v", err)
	}
	if len(pages) != 1 || pages[0].OCR {
		t.Fatalf("страницы: %+v", pages)
	}
	if renderer.cleanups != 1 {
		t.Fatalf("временные файлы не убраны: %d", renderer.cleanups)
	}
}

func TestPagesFromPDFPageLimit(t *testing.T) {
	contents := make([]string, 25)
	renderer := &fakeRenderer{}
	document := &Document{Enabled: true, MaxPages: 20, Timeout: time.Second, Engine: &fakeEngine{}, Renderer: renderer}
	_, err := document.PagesFromPDF(context.Background(), writePDF(t, contents...))
	if !errors.Is(err, ErrPageLimit) {
		t.Fatalf("ожидали ErrPageLimit, получили %v", err)
	}
	if !strings.Contains(err.Error(), EnvMaxPages) {
		t.Fatalf("в ошибке нет подсказки про %s: %v", EnvMaxPages, err)
	}
	if renderer.callCount() != 0 {
		t.Fatalf("рендеринг не должен запускаться: %v", renderer.calls)
	}
}

func TestPagesFromPDFDisabled(t *testing.T) {
	document := &Document{Enabled: false, Engine: &fakeEngine{}, Renderer: &fakeRenderer{}}
	_, err := document.PagesFromPDF(context.Background(), writePDF(t, ""))
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("ожидали ErrDisabled, получили %v", err)
	}
	for _, want := range []string{EnvEnabled, "UB-Mannheim.TesseractOCR", "oschwartz10612.Poppler", DocsPath} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в ошибке нет %q: %v", want, err)
		}
	}
}

func TestPagesFromPDFMissingTesseract(t *testing.T) {
	document := &Document{
		Enabled:  true,
		MaxPages: 20,
		Timeout:  time.Second,
		Engine:   &Tesseract{Path: "tesseract-does-not-exist-for-tests"},
		Renderer: &fakeRenderer{},
	}
	_, err := document.PagesFromPDF(context.Background(), writePDF(t, ""))
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("ожидали ErrUnavailable/ErrToolNotFound, получили %v", err)
	}
	if !strings.Contains(err.Error(), "UB-Mannheim.TesseractOCR") {
		t.Fatalf("нет подсказки установки: %v", err)
	}
}

func TestPagesFromPDFMissingRenderer(t *testing.T) {
	document := &Document{
		Enabled:  true,
		MaxPages: 20,
		Timeout:  time.Second,
		Engine:   &fakeEngine{},
		Renderer: &PDFRenderer{Path: "pdftoppm-does-not-exist-for-tests"},
	}
	_, err := document.PagesFromPDF(context.Background(), writePDF(t, ""))
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("ожидали ErrUnavailable/ErrToolNotFound, получили %v", err)
	}
	if !strings.Contains(err.Error(), "oschwartz10612.Poppler") {
		t.Fatalf("нет подсказки установки poppler: %v", err)
	}
}

func TestPagesFromPDFTimeout(t *testing.T) {
	renderer := &fakeRenderer{images: map[string][]string{"1-1": {"page-1.png"}}}
	document := &Document{
		Enabled:  true,
		MaxPages: 20,
		Timeout:  50 * time.Millisecond,
		Engine:   &fakeEngine{block: true},
		Renderer: renderer,
	}
	started := time.Now()
	_, err := document.PagesFromPDF(context.Background(), writePDF(t, ""))
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("ожидали ErrTimeout, получили %v", err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatalf("таймаут не сработал")
	}
	if renderer.cleanups != 1 {
		t.Fatalf("временные файлы не убраны при таймауте: %d", renderer.cleanups)
	}
}

func TestPagesFromPDFRendererError(t *testing.T) {
	document := &Document{
		Enabled:  true,
		MaxPages: 20,
		Timeout:  time.Second,
		Engine:   &fakeEngine{},
		Renderer: &fakeRenderer{renderErr: errors.New("pdftoppm упал")},
	}
	_, err := document.PagesFromPDF(context.Background(), writePDF(t, ""))
	if err == nil || !strings.Contains(err.Error(), "не удалось отрендерить страницы") {
		t.Fatalf("ожидали ошибку рендеринга, получили %v", err)
	}
}

func TestRecognizeImage(t *testing.T) {
	engine := &fakeEngine{texts: map[string]string{"scan.png": "УК-1. Способен осуществлять поиск информации."}}
	document := &Document{Enabled: true, Timeout: time.Second, Engine: engine}
	pages, err := document.RecognizeImage(context.Background(), "scan.png")
	if err != nil {
		t.Fatalf("RecognizeImage: %v", err)
	}
	if len(pages) != 1 || pages[0].Number != 1 || !pages[0].OCR || !strings.Contains(pages[0].Text, "УК-1") {
		t.Fatalf("страница изображения: %+v", pages)
	}
}

func TestRecognizeImageDisabled(t *testing.T) {
	document := &Document{Enabled: false, Engine: &fakeEngine{}}
	_, err := document.RecognizeImage(context.Background(), "scan.jpg")
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("ожидали ErrDisabled, получили %v", err)
	}
}

func TestRecognizeImageNoText(t *testing.T) {
	document := &Document{Enabled: true, Timeout: time.Second, Engine: &fakeEngine{}}
	_, err := document.RecognizeImage(context.Background(), "scan.jpeg")
	if !errors.Is(err, ErrNoText) {
		t.Fatalf("ожидали ErrNoText, получили %v", err)
	}
}

func TestRecognizeImageUnsupportedFormat(t *testing.T) {
	document := &Document{Enabled: true, Engine: &fakeEngine{}}
	_, err := document.RecognizeImage(context.Background(), "scan.gif")
	if !errors.Is(err, ErrUnsupportedImage) {
		t.Fatalf("ожидали ErrUnsupportedImage, получили %v", err)
	}
}

func TestFGOSExtractorUsesOCRForScan(t *testing.T) {
	renderer := &fakeRenderer{images: map[string][]string{"1-1": {"page-1.png"}}}
	engine := &fakeEngine{texts: map[string]string{"page-1.png": "УК-1. Способен осуществлять поиск информации."}}
	document := &Document{Enabled: true, MaxPages: 20, Timeout: time.Second, Engine: engine, Renderer: renderer}
	extractor := NewFGOSExtractor(fgos.NewExtractor(nil), document)

	pages, err := extractor.ExtractPages("scan.pdf", buildPDF(t, ""))
	if err != nil {
		t.Fatalf("ExtractPages: %v", err)
	}
	if len(pages) != 1 || !pages[0].OCR {
		t.Fatalf("OCR-страница не проброшена: %+v", pages)
	}

	contextPages, err := extractor.ExtractPagesContext(context.Background(), "scan.pdf", buildPDF(t, ""))
	if err != nil || len(contextPages) != 1 || !contextPages[0].OCR {
		t.Fatalf("ExtractPagesContext: pages=%+v err=%v", contextPages, err)
	}
}

func TestPageRanges(t *testing.T) {
	spans := pageRanges([]int{2, 3, 4, 7, 9, 10})
	want := []pageSpan{{2, 4}, {7, 7}, {9, 10}}
	if len(spans) != len(want) {
		t.Fatalf("диапазоны: %+v", spans)
	}
	for index := range want {
		if spans[index] != want[index] {
			t.Fatalf("диапазоны: %+v", spans)
		}
	}
}
