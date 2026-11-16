package ingestion

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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
	pdfDoc, err := ReadStandard(pdfPath, "pdf", "Пример")
	if err != nil || !strings.Contains(pdfDoc.Content, "Example standard text") {
		t.Fatalf("%+v %v", pdfDoc, err)
	}
	jsonPath := filepath.Join(dir, "standard.json")
	if err := os.WriteFile(jsonPath, []byte(`{"title":"Профстандарт","content":"Пример текста"}`), 0600); err != nil {
		t.Fatal(err)
	}
	doc, err := ReadStandard(jsonPath, "json", "")
	if err != nil || doc.Title != "Профстандарт" {
		t.Fatalf("%+v %v", doc, err)
	}
	if err = os.WriteFile(pdfPath, []byte("не PDF"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadStandard(pdfPath, "pdf", "Ошибка"); err == nil {
		t.Fatal("повреждённый PDF принят")
	}
}

func TestПовторныйИмпортПрофстандарта(t *testing.T) {
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна INGEST_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	key := fmt.Sprintf("test-%d", time.Now().UnixNano())
	defer pool.Exec(ctx, "DELETE FROM sources WHERE provider='file' AND type='standard' AND external_id=$1", "standard:"+key)
	doc := Standard{Title: "Профстандарт", Content: "Текст"}
	first, err := SaveStandard(ctx, pool, key, doc)
	if err != nil || first.Inserted != 1 {
		t.Fatalf("%+v %v", first, err)
	}
	second, err := SaveStandard(ctx, pool, key, doc)
	if err != nil || second.Skipped != 1 {
		t.Fatalf("%+v %v", second, err)
	}
}
