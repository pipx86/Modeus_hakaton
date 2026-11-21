package ingestion_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"activity-descriptor/internal/ingestion"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v4"
)

var errImportSQL = errors.New("тестовый сбой PostgreSQL")

func importPool(t *testing.T) pgxmock.PgxPoolIface {
	t.Helper()
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer pool.Close()
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})
	return pool
}

func TestVacancyImportClassifiesRecordsInOneTransaction(t *testing.T) {
	ctx := context.Background()
	pool := importPool(t)
	var existing pgtype.UUID
	if err := existing.Scan("805cd798-35d0-4637-ab57-00d9cd706624"); err != nil {
		t.Fatal(err)
	}
	when := pgtype.Timestamptz{Valid: true, Time: time.Date(2026, 11, 18, 7, 0, 0, 0, time.UTC)}
	items := []ingestion.Vacancy{
		{ID: " new ", Name: " Инженер ", Description: "Работа", PublishedAt: "2026-11-18T10:00:00+0300"},
		{ID: "same", Name: "Старшая роль", Description: "Без изменений", URL: "https://example.test/same"},
		{ID: "changed", Name: "Новое название", Description: "Изменено"},
	}
	lock := func(id string) {
		pool.ExpectExec("LockVacancyImport").WithArgs("hh:vacancy:" + id).WillReturnResult(pgxmock.NewResult("SELECT", 1))
	}
	current := func(title, body, link string) *pgxmock.Rows {
		return pgxmock.NewRows([]string{"id", "title", "content", "source_url", "vacancy_published_at"}).
			AddRow(existing, title, body, link, when)
	}
	pool.ExpectBegin()
	lock("changed")
	pool.ExpectQuery("GetVacancyForImport").WithArgs("changed").WillReturnRows(current("Старое название", "", ""))
	pool.ExpectExec("UpdateImportedVacancy").WithArgs("Новое название", "Изменено", "", pgtype.Timestamptz{}, existing).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	lock("new")
	pool.ExpectQuery("GetVacancyForImport").WithArgs("new").WillReturnError(pgx.ErrNoRows)
	pool.ExpectExec("CreateImportedVacancy").WithArgs("Инженер", "Работа", "", "new", when).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	lock("same")
	pool.ExpectQuery("GetVacancyForImport").WithArgs("same").
		WillReturnRows(current("Старшая роль", "Без изменений", items[1].URL))
	pool.ExpectCommit()
	got, err := ingestion.SaveVacancies(ctx, pool, items)
	if err != nil || got != (ingestion.Counts{Inserted: 1, Updated: 1, Skipped: 1}) {
		t.Fatalf("классификация вакансий: %+v, %v", got, err)
	}
}

func TestVacancyImportRollsBackRunOnInvalidDateOrWriteError(t *testing.T) {
	for _, fail := range []string{"формат даты", "запись", "фиксация"} {
		t.Run(fail, func(t *testing.T) {
			pool := importPool(t)
			items := []ingestion.Vacancy{{ID: "first", Name: "Инженер"}, {ID: "second", Name: "Аналитик"}}
			pool.ExpectBegin()
			for i, item := range items {
				if i == 1 && fail == "формат даты" {
					items[i].PublishedAt = "18.11.2026"
					break
				}
				pool.ExpectExec("LockVacancyImport").WithArgs("hh:vacancy:" + item.ID).
					WillReturnResult(pgxmock.NewResult("SELECT", 1))
				pool.ExpectQuery("GetVacancyForImport").WithArgs(item.ID).WillReturnError(pgx.ErrNoRows)
				insertion := pool.ExpectExec("CreateImportedVacancy").WithArgs(item.Name, "", "", item.ID, pgtype.Timestamptz{})
				if i == 1 && fail == "запись" {
					insertion.WillReturnError(errImportSQL)
				} else {
					insertion.WillReturnResult(pgxmock.NewResult("INSERT", 1))
				}
			}
			if fail == "фиксация" {
				pool.ExpectCommit().WillReturnError(errImportSQL)
			}
			pool.ExpectRollback()
			got, err := ingestion.SaveVacancies(context.Background(), pool, items)
			if err == nil || (fail != "формат даты" && !errors.Is(err, errImportSQL)) || got != (ingestion.Counts{}) {
				t.Fatalf("после отката: %+v, %v", got, err)
			}
		})
	}
}

func TestConfiguredFileImportUsesPersistentKeys(t *testing.T) {
	tmp := t.TempDir()
	csvPath, standardPath := filepath.Join(tmp, "vacancy.csv"), filepath.Join(tmp, "standard.json")
	if err := os.WriteFile(csvPath, []byte("id,name\nhh-fixture,Инженер\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(standardPath, []byte(`{"title":"Профстандарт","content":"Текст проверки"}`), 0600); err != nil {
		t.Fatal(err)
	}
	files := ingestion.ImportFiles{
		Vacancies: csvPath, VacancyFormat: "csv", Standard: standardPath, StandardKey: "unit-standard",
	}
	pool := importPool(t)
	pool.ExpectBegin()
	pool.ExpectExec("LockVacancyImport").WithArgs("hh:vacancy:hh-fixture").WillReturnResult(pgxmock.NewResult("SELECT", 1))
	pool.ExpectQuery("GetVacancyForImport").WithArgs("hh-fixture").WillReturnError(pgx.ErrNoRows)
	pool.ExpectExec("CreateImportedVacancy").WithArgs("Инженер", "", "", "hh-fixture", pgtype.Timestamptz{}).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()
	pool.ExpectBegin()
	pool.ExpectExec("pg_advisory_xact_lock").WithArgs("standard:unit-standard").WillReturnResult(pgxmock.NewResult("SELECT", 1))
	pool.ExpectQuery("FROM sources WHERE provider='file'").WithArgs("standard:unit-standard").
		WillReturnRows(pgxmock.NewRows([]string{"id", "title", "content", "source_url"}).
			AddRow("stored", "Профстандарт", "Текст проверки", ""))
	pool.ExpectCommit()
	got, err := files.Run(context.Background(), pool, []string{"vacancies", "standards"})
	want := ingestion.FileImportReport{Status: "ok", Ingested: 1, Vacancies: 1}
	if err != nil || got != want {
		t.Fatalf("отчёт импорта: %+v, %v", got, err)
	}
}

func TestTaxonomyImportWritesParentAfterAllItems(t *testing.T) {
	pool := importPool(t)
	data := ingestion.Taxonomy{Name: "Задачи", Version: "2026", Items: []ingestion.TaxonomyItem{
		{Code: "child", Title: "Операция", ParentCode: "parent"}, {Code: "parent", Title: "Функция"},
	}}
	for _, fail := range []bool{false, true} {
		pool.ExpectBegin()
		pool.ExpectExec("pg_advisory_xact_lock").WithArgs("taxonomy:tasks").WillReturnResult(pgxmock.NewResult("SELECT", 1))
		pool.ExpectQuery("INSERT INTO sources").WithArgs(data.Name, "tasks").
			WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("source"))
		expectExisting := pool.ExpectQuery("FROM taxonomies").WithArgs("source", data.Name, data.Version)
		if fail {
			expectExisting.WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("taxonomy"))
		} else {
			expectExisting.WillReturnError(pgx.ErrNoRows)
			pool.ExpectQuery("INSERT INTO taxonomies").WithArgs("source", data.Name, data.Version).
				WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("taxonomy"))
		}
		for _, item := range data.Items {
			pool.ExpectQuery("INSERT INTO taxonomy_items").WithArgs("taxonomy", item.Code, item.Title).
				WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(item.Code + "-id"))
		}
		update := pool.ExpectExec("UPDATE taxonomy_items SET parent_id").WithArgs("child-id", "parent-id")
		if fail {
			update.WillReturnError(errImportSQL)
			pool.ExpectRollback()
		} else {
			update.WillReturnResult(pgxmock.NewResult("UPDATE", 1))
			pool.ExpectExec("UPDATE taxonomy_items SET parent_id").WithArgs("parent-id", nil).
				WillReturnResult(pgxmock.NewResult("UPDATE", 1))
			pool.ExpectCommit()
		}
		id, err := ingestion.SaveTaxonomy(context.Background(), pool, " tasks ", data)
		if (fail && (!errors.Is(err, errImportSQL) || id != "")) || (!fail && (err != nil || id != "taxonomy")) {
			t.Fatalf("результат сохранения справочника: %q, %v", id, err)
		}
	}
}

func TestVacancyTrendMapsObservedMonthsAndUndatedRows(t *testing.T) {
	pool := importPool(t)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := from.AddDate(0, 2, 0)
	args := []any{"Инженер", pgtype.Timestamptz{Time: from, Valid: true}, pgtype.Timestamptz{Time: until, Valid: true}}
	pool.ExpectQuery("ListVacancyPublicationTrend").WithArgs(args...).
		WillReturnRows(pgxmock.NewRows([]string{"month", "provider", "vacancy_count"}).
			AddRow("", "hh", int64(2)).
			AddRow("2026-01", "hh", int64(4)).
			AddRow("2026-02", "hh", int64(7)))
	got, err := ingestion.VacancyPublicationTrend(context.Background(), pool, " Инженер ", from, until)
	want := ingestion.VacancyTrend{
		TitleQuery: "Инженер", From: "2026-01-01", Until: "2026-03-01", WithoutDate: 2,
		ObservedMonthly: []ingestion.VacancyMonth{{Month: "2026-01", Provider: "hh", Count: 4}, {Month: "2026-02", Provider: "hh", Count: 7}},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("помесячный отчёт: %+v, %v", got, err)
	}
	pool.ExpectQuery("ListVacancyPublicationTrend").WithArgs(args...).WillReturnError(errImportSQL)
	if _, err = ingestion.VacancyPublicationTrend(context.Background(), pool, "Инженер", from, until); !errors.Is(err, errImportSQL) {
		t.Fatalf("ошибка агрегации потеряна: %v", err)
	}
}

func TestEducationImportLinksOnlyResolvedCodes(t *testing.T) {
	pool := importPool(t)
	ctx := context.Background()
	program := ingestion.EducationProgram{
		Title: "Диагностика", Organization: " Колледж ", Code: " 01 ",
		Competencies: []ingestion.ProgramCompetency{{
			Code: "PC1", Title: "Проверять оборудование", TaxonomyCodes: []string{" found ", "missing-z"},
			KSA: []ingestion.ProgramKSA{{Kind: "skill", Title: "Калибровка", TaxonomyCodes: []string{"missing-a", "found"}}},
		}},
	}
	pool.ExpectBegin()
	pool.ExpectExec("pg_advisory_xact_lock").WithArgs("education:course").WillReturnResult(pgxmock.NewResult("SELECT", 1))
	pool.ExpectQuery("INSERT INTO sources").WithArgs(program.Title, "education:course").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("source"))
	pool.ExpectQuery("FROM education_programs WHERE program_key").WithArgs("course").
		WillReturnRows(pgxmock.NewRows([]string{"id", "program_key", "version"}))
	pool.ExpectQuery("FROM education_programs p JOIN program_sources").WithArgs("source").
		WillReturnRows(pgxmock.NewRows([]string{"id", "program_key", "version"}))
	pool.ExpectQuery("INSERT INTO education_programs").WithArgs(program.Title, "Колледж", "01", "course").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("program"))
	pool.ExpectExec("INSERT INTO program_sources").WithArgs("program", "source").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectQuery("INSERT INTO program_competencies").WithArgs("program", "PC1", "Проверять оборудование", "").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("competency"))
	lookup := func(code string) *pgxmock.ExpectedQuery {
		return pool.ExpectQuery("FROM taxonomy_items WHERE taxonomy_id").WithArgs("taxonomy", code)
	}
	lookup("found").WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("item"))
	pool.ExpectExec("INSERT INTO competency_taxonomy_links").WithArgs("competency", "item").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	lookup("missing-z").WillReturnError(pgx.ErrNoRows)
	pool.ExpectQuery("FROM competency_ksa WHERE competency_id").WithArgs("competency", "skill", "Калибровка").WillReturnError(pgx.ErrNoRows)
	pool.ExpectQuery("INSERT INTO competency_ksa").WithArgs("competency", "skill", "Калибровка").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("ksa"))
	lookup("missing-a").WillReturnError(pgx.ErrNoRows)
	lookup("found").WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("item"))
	pool.ExpectExec("INSERT INTO ksa_taxonomy_links").WithArgs("ksa", "item").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectQuery("FROM program_competencies c WHERE").WithArgs("program").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	pool.ExpectQuery("FROM competency_ksa k JOIN").WithArgs("program").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	pool.ExpectCommit()
	got, err := ingestion.SaveEducation(ctx, pool, " course ", "taxonomy", program)
	if err != nil || got.ProgramID != "program" || got.UnmatchedCompetencies != 0 || got.UnmatchedKSA != 0 ||
		!reflect.DeepEqual(got.UnknownTaxonomyCodes, []string{"missing-a", "missing-z"}) {
		t.Fatalf("отчёт программы: %+v, %v", got, err)
	}
	pool.ExpectBegin()
	pool.ExpectExec("pg_advisory_xact_lock").WithArgs("education:course").WillReturnResult(pgxmock.NewResult("SELECT", 1))
	pool.ExpectQuery("INSERT INTO sources").WithArgs(program.Title, "education:course").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("source"))
	pool.ExpectQuery("FROM education_programs WHERE program_key").WithArgs("course").
		WillReturnRows(pgxmock.NewRows([]string{"id", "program_key", "version"}))
	pool.ExpectQuery("FROM education_programs p JOIN program_sources").WithArgs("source").
		WillReturnRows(pgxmock.NewRows([]string{"id", "program_key", "version"}).
			AddRow("program", "", 1).AddRow("other", "", 1))
	pool.ExpectRollback()
	if report, err := ingestion.SaveEducation(ctx, pool, "course", "taxonomy", program); err == nil || report.ProgramID != "" {
		t.Fatalf("неоднозначный источник не должен обновляться: %+v, %v", report, err)
	}
}
