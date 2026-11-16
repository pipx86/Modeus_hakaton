package ingestion

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestФорматПрограммы(t *testing.T) {
	valid := `{"title":"Программа","competencies":[{"code":"K1","title":"Компетенция","ksa":[{"kind":"knowledge","title":"Знание"}]}]}`
	if result, err := ReadEducation(strings.NewReader(valid)); err != nil || len(result.Competencies) != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	for _, invalid := range []string{
		`{"title":"Программа","competencies":[{"title":"Нет кода"}]}`,
		`{"title":"Программа","competencies":[{"code":"K","title":"X","ksa":[{"kind":"other","title":"X"}]}]}`,
		`{"title":"Программа","competencies":[{"code":"K","title":"X"},{"code":"K","title":"Y"}]}`,
	} {
		if _, err := ReadEducation(strings.NewReader(invalid)); err == nil {
			t.Fatalf("ошибочный документ принят: %s", invalid)
		}
	}
}

func TestРазмеченныйТекстПрограммы(t *testing.T) {
	doc, err := ReadEducationText("Компетенция: К1 | Проектирование\nЗнание: Модели\nУмение: Анализировать\nНавык: Документировать", "Учебная программа")
	if err != nil || len(doc.Competencies) != 1 || len(doc.Competencies[0].KSA) != 3 {
		t.Fatalf("%+v %v", doc, err)
	}
	if _, err = ReadEducationText("Знание: без компетенции", "Программа"); err == nil {
		t.Fatal("принято знание без компетенции")
	}
	if _, err = ReadEducationText("произвольный текст", "Программа"); err == nil {
		t.Fatal("принят неразмеченный текст")
	}
}

func TestПовторныйИмпортПрограммы(t *testing.T) {
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
	defer func() {
		pool.Exec(ctx, "DELETE FROM education_programs WHERE id IN (SELECT program_id FROM program_sources WHERE source_id IN (SELECT id FROM sources WHERE provider='file' AND external_id=$1))", "education:"+key)
		pool.Exec(ctx, "DELETE FROM sources WHERE provider='file' AND external_id=$1", "education:"+key)
	}()
	data, err := ReadEducation(strings.NewReader(`{"title":"Программа","competencies":[{"code":"K","title":"Компетенция","taxonomy_codes":["unknown"],"ksa":[{"kind":"knowledge","title":"Знание"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	first, err := SaveEducation(ctx, pool, key, "", data)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SaveEducation(ctx, pool, key, "", data)
	if err != nil {
		t.Fatal(err)
	}
	if first.ProgramID != second.ProgramID || second.UnmatchedCompetencies != 1 || second.UnmatchedKSA != 1 || len(second.UnknownTaxonomyCodes) != 1 {
		t.Fatalf("%+v %+v", first, second)
	}
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM competency_ksa k JOIN program_competencies c ON c.id=k.competency_id WHERE c.program_id=$1", first.ProgramID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("%d %v", count, err)
	}
}

func TestСопоставлениеПрограммы(t *testing.T) {
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
	key := fmt.Sprintf("match-%d", time.Now().UnixNano())
	defer func() {
		pool.Exec(ctx, "DELETE FROM education_programs WHERE id IN (SELECT program_id FROM program_sources WHERE source_id IN (SELECT id FROM sources WHERE provider='file' AND external_id=$1))", "education:"+key)
		pool.Exec(ctx, "DELETE FROM sources WHERE provider='file' AND external_id=$1", "education:"+key)
		pool.Exec(ctx, "DELETE FROM taxonomies WHERE source_id IN (SELECT id FROM sources WHERE provider='file' AND external_id=$1)", key)
		pool.Exec(ctx, "DELETE FROM sources WHERE provider='file' AND external_id=$1", key)
	}()
	tax, err := ReadTaxonomy(strings.NewReader(`{"name":"Справочник","items":[{"code":"A","title":"Анализ"}]}`), "json", "")
	if err != nil {
		t.Fatal(err)
	}
	taxID, err := SaveTaxonomy(ctx, pool, key, tax)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ReadEducation(strings.NewReader(`{"title":"Программа","competencies":[{"code":"K","title":"Компетенция","taxonomy_codes":["A"],"ksa":[{"kind":"knowledge","title":"Знание","taxonomy_codes":["A"]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	report, err := SaveEducation(ctx, pool, key, taxID, doc)
	if err != nil {
		t.Fatal(err)
	}
	if report.UnmatchedCompetencies != 0 || report.UnmatchedKSA != 0 || len(report.UnknownTaxonomyCodes) != 0 {
		t.Fatalf("%+v", report)
	}
}
