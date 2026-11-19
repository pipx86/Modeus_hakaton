package ingestion_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"activity-descriptor/internal/ingestion"
)

func demoPDF() []byte {
	stream := "BT /F1 12 Tf 50 700 Td (Example standard text) Tj ET"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
	}
	s := "%PDF-1.4\n"
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, len(s))
		s += fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := len(s)
	s += fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		s += fmt.Sprintf("%010d 00000 n \n", offset)
	}
	s += fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return []byte(s)
}

func TestЧтениеПрофстандарта(t *testing.T) {
	dir := t.TempDir()
	pdfPath := filepath.Join(dir, "standard.pdf")
	if err := os.WriteFile(pdfPath, demoPDF(), 0600); err != nil {
		t.Fatal(err)
	}
	pdfDoc, err := ingestion.ReadStandard(pdfPath, "pdf", "Пример")
	if err != nil || !strings.Contains(pdfDoc.Content, "Example standard text") {
		t.Fatalf("%+v %v", pdfDoc, err)
	}
	jsonPath := filepath.Join(dir, "standard.json")
	if err := os.WriteFile(jsonPath, []byte(`{"title":"Профстандарт","content":"Пример текста"}`), 0600); err != nil {
		t.Fatal(err)
	}
	doc, err := ingestion.ReadStandard(jsonPath, "json", "")
	if err != nil || doc.Title != "Профстандарт" {
		t.Fatalf("%+v %v", doc, err)
	}
	if err = os.WriteFile(pdfPath, []byte("не PDF"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = ingestion.ReadStandard(pdfPath, "pdf", "Ошибка"); err == nil {
		t.Fatal("повреждённый PDF принят")
	}
}
