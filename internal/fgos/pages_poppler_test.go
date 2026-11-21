package fgos

import (
	"testing"
)

func TestSplitPopplerPages(t *testing.T) {
	pages := splitPopplerPages("первая\f\fтретья\f")
	if len(pages) != 3 {
		t.Fatalf("страниц: %d, ожидали 3", len(pages))
	}
	if pages[0].Number != 1 || pages[0].Text != "первая" {
		t.Fatalf("первая страница: %+v", pages[0])
	}
	if pages[1].Number != 2 || pages[1].Text != "" {
		t.Fatalf("вторая страница: %+v", pages[1])
	}
	if pages[2].Number != 3 || pages[2].Text != "третья" {
		t.Fatalf("третья страница: %+v", pages[2])
	}
	if got := splitPopplerPages("   \f \f"); got != nil {
		t.Fatalf("пустой вывод должен давать nil, получено %+v", got)
	}
}

func TestPopplerBinaryHonoursOffSwitch(t *testing.T) {
	t.Setenv("FGOS_PDFTOTEXT", "off")
	if got := popplerBinary(); got != "" {
		t.Fatalf("при off ожидали пустой путь, получено %q", got)
	}
	t.Setenv("FGOS_PDFTOTEXT", "false")
	if got := popplerBinary(); got != "" {
		t.Fatalf("при false ожидали пустой путь, получено %q", got)
	}
}

func TestExtractPDFPagesWithPopplerDisabled(t *testing.T) {
	t.Setenv("FGOS_PDFTOTEXT", "off")
	if pages, ok := extractPDFPagesWithPoppler([]byte("%PDF-1.4")); ok || pages != nil {
		t.Fatalf("при off ожидали отказ, получено pages=%v ok=%v", pages, ok)
	}
}
