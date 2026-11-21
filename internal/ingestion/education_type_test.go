package ingestion

import (
	"context"
	"testing"

	"github.com/pashagolub/pgxmock/v4"
)

// TestSaveEducationPersistsCompetencyType проверяет через pgxmock, что тип
// компетенции передаётся в INSERT program_competencies и обновляется при
// повторном импорте, без подключения к PostgreSQL.
func TestSaveEducationPersistsCompetencyType(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	pool.MatchExpectationsInOrder(true)

	pool.ExpectBegin()
	pool.ExpectExec("pg_advisory_xact_lock").WithArgs("education:key").
		WillReturnResult(pgxmock.NewResult("SELECT", 0))
	pool.ExpectQuery("INSERT INTO sources").WithArgs("Программа", "education:key").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("source-1"))
	pool.ExpectQuery("FROM education_programs WHERE program_key").WithArgs("key").
		WillReturnRows(pgxmock.NewRows([]string{"id", "program_key", "version"}))
	pool.ExpectQuery("FROM education_programs p JOIN program_sources").WithArgs("source-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "program_key", "version"}))
	pool.ExpectQuery("INSERT INTO education_programs").WithArgs("Программа", "", "P1", "key").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("program-1"))
	pool.ExpectExec("INSERT INTO program_sources").WithArgs("program-1", "source-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectQuery("INSERT INTO program_competencies").WithArgs("program-1", "K1", "Компетенция", "professional").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("competency-1"))
	pool.ExpectQuery("FROM program_competencies c").WithArgs("program-1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	pool.ExpectQuery("FROM competency_ksa k").WithArgs("program-1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	pool.ExpectCommit()

	program := EducationProgram{
		Code:  "P1",
		Title: "Программа",
		Competencies: []ProgramCompetency{
			{Code: "K1", Title: "Компетенция", Type: "professional"},
		},
	}
	report, err := SaveEducation(context.Background(), pool, "key", "", program)
	if err != nil {
		t.Fatalf("SaveEducation: %v", err)
	}
	if report.ProgramID != "program-1" || report.UnmatchedCompetencies != 1 || report.UnmatchedKSA != 0 {
		t.Fatalf("отчёт: %+v", report)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("ожидания pgxmock: %v", err)
	}
}

func TestSaveEducationRejectsUnknownCompetencyType(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	pool.ExpectBegin()
	pool.ExpectExec("pg_advisory_xact_lock").WithArgs("education:key").
		WillReturnResult(pgxmock.NewResult("SELECT", 0))
	pool.ExpectQuery("INSERT INTO sources").WithArgs("Программа", "education:key").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("source-1"))
	pool.ExpectQuery("FROM education_programs WHERE program_key").WithArgs("key").
		WillReturnRows(pgxmock.NewRows([]string{"id", "program_key", "version"}))
	pool.ExpectQuery("FROM education_programs p JOIN program_sources").WithArgs("source-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "program_key", "version"}))
	pool.ExpectQuery("INSERT INTO education_programs").WithArgs("Программа", "", "", "key").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("program-1"))
	pool.ExpectExec("INSERT INTO program_sources").WithArgs("program-1", "source-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectRollback()

	_, err = SaveEducation(context.Background(), pool, "key", "", EducationProgram{
		Title: "Программа",
		Competencies: []ProgramCompetency{
			{Code: "K1", Title: "Компетенция", Type: "неизвестный"},
		},
	})
	if err == nil {
		t.Fatal("ожидали ошибку неизвестного type")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("ожидания pgxmock: %v", err)
	}
}
