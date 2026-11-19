package ingestion

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidFileSources    = errors.New("нужен список вакансий и/или профстандартов без повторов")
	ErrFileSourceUnavailable = errors.New("для выбранного источника не настроен файловый импорт")
)

type ImportFiles struct {
	Vacancies      string
	VacancyFormat  string
	Standard       string
	StandardFormat string
	StandardKey    string
	StandardTitle  string
}

type FileImportReport struct {
	Status    string `json:"status"`
	Ingested  int    `json:"ingested"`
	Vacancies int    `json:"vacancies"`
	Standards int    `json:"standards"`
}

func (files ImportFiles) Run(ctx context.Context, pool *pgxpool.Pool, sources []string) (FileImportReport, error) {
	var vacancies, standards bool
	if len(sources) == 0 || len(sources) > 2 {
		return FileImportReport{}, ErrInvalidFileSources
	}
	for _, source := range sources {
		switch {
		case source == "vacancies" && !vacancies:
			vacancies = true
		case source == "standards" && !standards:
			standards = true
		default:
			return FileImportReport{}, ErrInvalidFileSources
		}
	}
	if (vacancies && files.Vacancies == "") || (standards && (files.Standard == "" || files.StandardKey == "")) {
		return FileImportReport{}, ErrFileSourceUnavailable
	}

	var vacancyItems []Vacancy
	var standard Standard
	var err error
	if vacancies {
		format := files.VacancyFormat
		if format == "" {
			format = "json"
		}
		vacancyItems, err = ReadVacancyFile(files.Vacancies, format)
		if err != nil {
			return FileImportReport{}, err
		}
	}
	if standards {
		format := files.StandardFormat
		if format == "" {
			format = "json"
		}
		standard, err = ReadStandard(files.Standard, format, files.StandardTitle)
		if err != nil {
			return FileImportReport{}, err
		}
	}
	report := FileImportReport{Status: "ok"}
	if vacancies {
		counts, err := SaveVacancies(ctx, pool, vacancyItems)
		if err != nil {
			return FileImportReport{}, err
		}
		report.Vacancies = counts.Inserted + counts.Updated
	}
	if standards {
		counts, err := SaveStandard(ctx, pool, files.StandardKey, standard)
		if err != nil {
			return FileImportReport{}, err
		}
		report.Standards = counts.Inserted + counts.Updated
	}
	// Загрузчики сохраняют записи по постоянным ключам, поэтому повтор безопасен.
	report.Ingested = report.Vacancies + report.Standards
	return report, nil
}
