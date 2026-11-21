package ingestion

import (
	"context"
	"testing"

	"github.com/pashagolub/pgxmock/v4"
)

// TestSaveEducationIncrementsVersionOnRepublish проверяет правило версии на
// уровне SQL без PostgreSQL: найденная по program_key программа обновляется с
// version+1, а источник переиспользуется.
func TestSaveEducationIncrementsVersionOnRepublish(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	pool.MatchExpectationsInOrder(true)

	const key = "mirea/09.03.01/2024/full-time"
	pool.ExpectBegin()
	pool.ExpectExec("pg_advisory_xact_lock").WithArgs("education:" + key).
		WillReturnResult(pgxmock.NewResult("SELECT", 0))
	pool.ExpectQuery("INSERT INTO sources").WithArgs("Программа", "education:"+key).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("source-1"))
	pool.ExpectQuery("FROM education_programs WHERE program_key").WithArgs(key).
		WillReturnRows(pgxmock.NewRows([]string{"id", "program_key", "version"}).AddRow("program-1", key, 2))
	pool.ExpectExec("UPDATE education_programs").
		WithArgs("program-1", "Программа", "", "P1", key, 3).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	pool.ExpectExec("INSERT INTO program_sources").WithArgs("program-1", "source-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectQuery("FROM program_competencies c").WithArgs("program-1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	pool.ExpectQuery("FROM competency_ksa k").WithArgs("program-1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	pool.ExpectCommit()

	report, err := SaveEducation(context.Background(), pool, key, "", EducationProgram{Title: "Программа", Code: "P1"})
	if err != nil {
		t.Fatalf("SaveEducation: %v", err)
	}
	if report.ProgramID != "program-1" {
		t.Fatalf("ProgramID = %q", report.ProgramID)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("ожидания pgxmock: %v", err)
	}
}

// TestSaveEducationCreatesVersionOne проверяет, что новая программа получает
// version=1, а не 0/2.
func TestSaveEducationCreatesVersionOne(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	pool.MatchExpectationsInOrder(true)

	const key = "mirea/09.03.01/2024/full-time"
	pool.ExpectBegin()
	pool.ExpectExec("pg_advisory_xact_lock").WithArgs("education:" + key).
		WillReturnResult(pgxmock.NewResult("SELECT", 0))
	pool.ExpectQuery("INSERT INTO sources").WithArgs("Программа", "education:"+key).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("source-1"))
	pool.ExpectQuery("FROM education_programs WHERE program_key").WithArgs(key).
		WillReturnRows(pgxmock.NewRows([]string{"id", "program_key", "version"}))
	pool.ExpectQuery("FROM education_programs p JOIN program_sources").WithArgs("source-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "program_key", "version"}))
	pool.ExpectQuery("INSERT INTO education_programs").WithArgs("Программа", "", "P1", key).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("program-1"))
	pool.ExpectExec("INSERT INTO program_sources").WithArgs("program-1", "source-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectQuery("FROM program_competencies c").WithArgs("program-1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	pool.ExpectQuery("FROM competency_ksa k").WithArgs("program-1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	pool.ExpectCommit()

	report, err := SaveEducation(context.Background(), pool, key, "", EducationProgram{Title: "Программа", Code: "P1"})
	if err != nil {
		t.Fatalf("SaveEducation: %v", err)
	}
	if report.ProgramID != "program-1" {
		t.Fatalf("ProgramID = %q", report.ProgramID)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("ожидания pgxmock: %v", err)
	}
}
