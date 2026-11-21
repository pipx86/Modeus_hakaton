package ocr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPDFRendererBinaryNotFound(t *testing.T) {
	renderer := &PDFRenderer{Path: "pdftoppm-does-not-exist-for-tests"}
	paths, cleanup, err := renderer.Render(context.Background(), "document.pdf", 1, 1)
	if !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("ожидали ErrToolNotFound, получили %v", err)
	}
	if paths != nil || cleanup != nil {
		t.Fatalf("при ошибке не должно быть файлов: paths=%v, cleanup задан=%t", paths, cleanup != nil)
	}
	for _, want := range []string{"oschwartz10612.Poppler", EnvPDFToPPM, DocsPath} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("в ошибке нет %q: %v", want, err)
		}
	}
}

func TestPDFRendererRejectsInvalidRange(t *testing.T) {
	renderer := &PDFRenderer{}
	if _, _, err := renderer.Render(context.Background(), "document.pdf", 0, 1); err == nil {
		t.Fatal("ожидали ошибку для страницы 0")
	}
	if _, _, err := renderer.Render(context.Background(), "document.pdf", 5, 3); err == nil {
		t.Fatal("ожидали ошибку для перевёрнутого диапазона")
	}
}

func TestPDFRendererDefaults(t *testing.T) {
	renderer := &PDFRenderer{}
	if renderer.binaryPath() != DefaultPDFToPPM || renderer.dpi() != DefaultDPI {
		t.Fatalf("значения по умолчанию: %q %d", renderer.binaryPath(), renderer.dpi())
	}
}

func TestCollectRenderedPagesSortsByPageNumber(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"page-7.png", "page-05.png", "page-6.png"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("png"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	paths, err := collectRenderedPages(directory, 5, 7)
	if err != nil {
		t.Fatalf("collectRenderedPages: %v", err)
	}
	want := []string{
		filepath.Join(directory, "page-05.png"),
		filepath.Join(directory, "page-6.png"),
		filepath.Join(directory, "page-7.png"),
	}
	if len(paths) != len(want) {
		t.Fatalf("пути: %v", paths)
	}
	for index := range want {
		if paths[index] != want[index] {
			t.Fatalf("порядок страниц: %v", paths)
		}
	}
}

func TestCollectRenderedPagesReportsMissing(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "page-1.png"), []byte("png"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := collectRenderedPages(directory, 1, 3); err == nil {
		t.Fatal("ожидали ошибку о недостающих страницах")
	}
}
