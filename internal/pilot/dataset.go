package pilot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Dataset — снимок данных для офлайн-аудита пилота: источники по профессии,
// выбранная программа, её компетенции, KSA и связи с таксономией. Снимок
// повторяет то, что Store читает из PostgreSQL, и позволяет проверять карточки
// 4 и 5 без базы: вручную, в CI или на машине без доступа к стенду.
//
// Снимок считается уже отфильтрованным по профессии: поле sources не хранит
// профессию, поэтому SourcesByProfession возвращает все источники файла.
type Dataset struct {
	Sources         []SourceRow           `json:"sources"`
	Program         *DatasetProgram       `json:"program,omitempty"`
	Competencies    []DatasetCompetency   `json:"competencies,omitempty"`
	KSA             []DatasetKSA          `json:"ksa,omitempty"`
	TaxonomyLinks   []DatasetTaxonomyLink `json:"taxonomy_links,omitempty"`
	AnalyzerMatches []string              `json:"analyzer_matches,omitempty"`
}

// DatasetProgram — программа в снимке. В отличие от базы, ID может быть
// произвольной строкой: снимок может быть обезличен или собран вручную.
type DatasetProgram struct {
	ID                  string `json:"id"`
	Title               string `json:"title"`
	HasLearningOutcomes bool   `json:"has_learning_outcomes,omitempty"`
	HasDisciplines      bool   `json:"has_disciplines,omitempty"`
}

// DatasetCompetency — компетенция выбранной программы.
type DatasetCompetency struct {
	ID    string `json:"id"`
	Code  string `json:"code,omitempty"`
	Title string `json:"title,omitempty"`
}

// DatasetKSA — знание, умение или навык программы. CompetencyID необязателен,
// но если указан, должен ссылаться на компетенцию снимка.
type DatasetKSA struct {
	ID           string `json:"id"`
	CompetencyID string `json:"competency_id,omitempty"`
	Kind         string `json:"kind,omitempty"` // knowledge | skill | ability
	Title        string `json:"title,omitempty"`
}

// DatasetTaxonomyLink — связь компетенции или KSA с элементом таксономии.
// Заполнено ровно одно из полей CompetencyID и KSAID.
type DatasetTaxonomyLink struct {
	CompetencyID   string `json:"competency_id,omitempty"`
	KSAID          string `json:"ksa_id,omitempty"`
	TaxonomyItemID string `json:"taxonomy_item_id"`
}

// FixtureProfession — профессия встроенного демонстрационного набора.
const FixtureProfession = "Аналитик данных"

// FixtureProgramID — ID программы встроенного демонстрационного набора.
// ID не UUID: офлайн-снимок не обязан повторять формат базы.
const FixtureProgramID = "program-data-analyst"

// Reader — общий интерфейс источников данных аудита. Его реализуют Store
// (PostgreSQL) и Dataset (JSON-снимок), поэтому команда pilot-audit не
// разветвляется на две копии логики отчётов.
type Reader interface {
	SourcesByProfession(ctx context.Context, profession string) ([]SourceRow, error)
	MatchedSourceIDs(ctx context.Context, profession string) ([]string, error)
	LoadProgramFacts(ctx context.Context, programID string) (ProgramFacts, error)
}

var (
	_ Reader = (*Dataset)(nil)
	_ Reader = (*Store)(nil)
)

// LoadDataset читает и валидирует снимок данных из JSON-файла. Разбор строгий:
// неизвестные поля и данные после JSON считаются ошибкой, чтобы опечатка в
// снимке не превратилась в тихо неполный отчёт.
func LoadDataset(path string) (*Dataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pilot: чтение снимка данных: %w", err)
	}
	// Windows-редакторы и PowerShell нередко сохраняют UTF-8 с BOM. Для JSON
	// BOM — не данные, поэтому снимаем его до строгого разбора.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	var dataset Dataset
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&dataset); err != nil {
		return nil, fmt.Errorf("pilot: разбор снимка данных %s: %w", path, err)
	}
	var tail json.RawMessage
	if err := decoder.Decode(&tail); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("pilot: снимок данных %s: лишние данные после JSON", path)
	}
	if err := dataset.Validate(); err != nil {
		return nil, fmt.Errorf("pilot: снимок данных %s: %w", path, err)
	}
	return &dataset, nil
}

// Validate проверяет ссылочную целостность снимка. Пустой снимок допустим:
// это честный результат «данных нет», а вердикт «требует внимания» выносит
// аудит, а не загрузчик.
func (d *Dataset) Validate() error {
	if d == nil {
		return errors.New("снимок данных не передан")
	}

	sourceIDs := make(map[string]string, len(d.Sources))
	for i, row := range d.Sources {
		id := normalizeID(row.ID)
		if id == "" {
			return fmt.Errorf("источник #%d без id", i+1)
		}
		if previous, duplicate := sourceIDs[id]; duplicate {
			return fmt.Errorf("повторяющийся id источника %q (уже был в источнике %q)", row.ID, previous)
		}
		sourceIDs[id] = row.ID
	}
	for i, id := range d.AnalyzerMatches {
		key := normalizeID(id)
		if key == "" {
			return fmt.Errorf("analyzer_matches[%d] пустой id", i)
		}
		if _, ok := sourceIDs[key]; !ok {
			return fmt.Errorf("analyzer_matches ссылается на неизвестный источник %q", id)
		}
	}

	if d.Program != nil {
		if strings.TrimSpace(d.Program.ID) == "" {
			return errors.New("у программы пустой id")
		}
		if strings.TrimSpace(d.Program.Title) == "" {
			return errors.New("у программы пустое название (title)")
		}
	} else if len(d.Competencies) > 0 || len(d.KSA) > 0 || len(d.TaxonomyLinks) > 0 {
		return errors.New("есть компетенции, KSA или связи с таксономией, но нет программы")
	}

	competencyIDs := make(map[string]string, len(d.Competencies))
	for i, competency := range d.Competencies {
		id := normalizeID(competency.ID)
		if id == "" {
			return fmt.Errorf("компетенция #%d без id", i+1)
		}
		if previous, duplicate := competencyIDs[id]; duplicate {
			return fmt.Errorf("повторяющийся id компетенции %q (уже была %q)", competency.ID, previous)
		}
		competencyIDs[id] = competency.ID
	}

	ksaIDs := make(map[string]string, len(d.KSA))
	for i, ksa := range d.KSA {
		id := normalizeID(ksa.ID)
		if id == "" {
			return fmt.Errorf("KSA #%d без id", i+1)
		}
		if previous, duplicate := ksaIDs[id]; duplicate {
			return fmt.Errorf("повторяющийся id KSA %q (уже был %q)", ksa.ID, previous)
		}
		ksaIDs[id] = ksa.ID
		if competencyID := normalizeID(ksa.CompetencyID); competencyID != "" {
			if _, ok := competencyIDs[competencyID]; !ok {
				return fmt.Errorf("KSA %q ссылается на неизвестную компетенцию %q", ksa.ID, ksa.CompetencyID)
			}
		}
		if ksa.Kind != "" && !validKSAKind(ksa.Kind) {
			return fmt.Errorf("KSA %q имеет неизвестный kind %q", ksa.ID, ksa.Kind)
		}
	}

	for i, link := range d.TaxonomyLinks {
		hasCompetency := normalizeID(link.CompetencyID) != ""
		hasKSA := normalizeID(link.KSAID) != ""
		switch {
		case hasCompetency && hasKSA:
			return fmt.Errorf("связь с таксономией #%d задаёт одновременно competency_id и ksa_id", i+1)
		case !hasCompetency && !hasKSA:
			return fmt.Errorf("связь с таксономией #%d не задаёт ни competency_id, ни ksa_id", i+1)
		case hasCompetency:
			if _, ok := competencyIDs[normalizeID(link.CompetencyID)]; !ok {
				return fmt.Errorf("связь с таксономией #%d ссылается на неизвестную компетенцию %q", i+1, link.CompetencyID)
			}
		case hasKSA:
			if _, ok := ksaIDs[normalizeID(link.KSAID)]; !ok {
				return fmt.Errorf("связь с таксономией #%d ссылается на неизвестный KSA %q", i+1, link.KSAID)
			}
		}
		if strings.TrimSpace(link.TaxonomyItemID) == "" {
			return fmt.Errorf("связь с таксономией #%d без taxonomy_item_id", i+1)
		}
	}
	return nil
}

// SourcesByProfession возвращает источники снимка. Профессия проверяется
// только на непустоту: снимок уже ограничен одной профессией, и повторная
// фильтрация по названию могла бы выбросить валидные источники.
func (d *Dataset) SourcesByProfession(_ context.Context, profession string) ([]SourceRow, error) {
	if strings.TrimSpace(profession) == "" {
		return nil, errors.New("профессия не задана")
	}
	return append([]SourceRow(nil), d.Sources...), nil
}

// MatchedSourceIDs возвращает совпадения анализатора, сохранённые в снимке.
// В офлайн-режиме адаптер вакансий не запускается, поэтому список — часть
// снимка, а не результат живого поиска. Пустой список означает, что снимок не
// фиксировал совпадения.
func (d *Dataset) MatchedSourceIDs(_ context.Context, profession string) ([]string, error) {
	if strings.TrimSpace(profession) == "" {
		return nil, errors.New("профессия не задана")
	}
	return append([]string(nil), d.AnalyzerMatches...), nil
}

// LoadProgramFacts собирает факты программы из снимка. Если ID не совпадает с
// программой снимка, возвращается Found=false без ошибки — так же, как Store
// для отсутствующей программы.
func (d *Dataset) LoadProgramFacts(_ context.Context, programID string) (ProgramFacts, error) {
	programID = strings.TrimSpace(programID)
	if programID == "" {
		return ProgramFacts{}, errors.New("ID программы не задан")
	}
	if d.Program == nil || strings.TrimSpace(d.Program.ID) != programID {
		return ProgramFacts{ID: programID}, nil
	}
	return ProgramFacts{
		ID:                  d.Program.ID,
		Title:               d.Program.Title,
		Found:               true,
		Competencies:        len(d.Competencies),
		KSA:                 len(d.KSA),
		TaxonomyLinks:       len(d.TaxonomyLinks),
		HasLearningOutcomes: d.Program.HasLearningOutcomes,
		HasDisciplines:      d.Program.HasDisciplines,
	}, nil
}

// Fixture возвращает встроенный демонстрационный набор: 6 источников четырёх
// типов, одну программу, 3 компетенции, 5 KSA и 3 связи с таксономией.
// Данные вымышленные и нужны только для проверки самой команды: это не
// пилотный набор и не эталон.
func Fixture() *Dataset {
	created := func(day int) time.Time {
		return time.Date(2026, 11, day, 10, 0, 0, 0, time.UTC)
	}
	return &Dataset{
		Sources: []SourceRow{
			{
				ID:        "src-fixture-vacancy-1",
				Type:      "vacancy",
				Title:     "Аналитик данных — вакансия hh",
				Content:   "Требования: SQL на уровне уверенных JOIN и оконных функций, опыт построения дашбордов, работа с продуктовыми метриками.",
				URL:       "https://hh.example/vacancy/1001",
				CreatedAt: created(10),
			},
			{
				ID:          "src-fixture-vacancy-2",
				Type:        "vacancy",
				Title:       "Старший аналитик данных — вакансия hh",
				Content:     "Задачи: поддерживать слой отчётности, проверять качество данных, согласовывать методики расчёта метрик с продуктовыми командами.",
				URL:         "https://hh.example/vacancy/1002",
				CreatedAt:   created(10),
				PublishedAt: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
			},
			{
				ID:        "src-fixture-interview-1",
				Type:      "interview",
				Title:     "Интервью методиста с аналитиком данных",
				Content:   "Респондент описывает роль, регулярную отчётность, построение дашбордов и проверку выгрузок перед публикацией.",
				URL:       "https://wiki.example/interviews/da-01",
				CreatedAt: created(11),
			},
			{
				ID:        "src-fixture-interview-2",
				Type:      "interview",
				Title:     "Интервью с руководителем аналитики",
				Content:   "Руководитель называет ключевой функцией аналитическую поддержку решений и требует согласовывать методику расчёта метрик.",
				URL:       "https://wiki.example/interviews/da-02",
				CreatedAt: created(11),
			},
			{
				ID:        "src-fixture-standard-1",
				Type:      "standard",
				Title:     "Профессиональный стандарт «Аналитик данных»",
				Content:   "Стандарт описывает подготовку отчётности, работу с хранилищем данных и аналитическую поддержку управленческих решений.",
				URL:       "https://profstandart.example/da",
				CreatedAt: created(12),
			},
			{
				ID:        "src-fixture-document-1",
				Type:      "document",
				Title:     "Регламент отчётности продуктовой команды",
				Content:   "Регламент фиксирует порядок публикации дашбордов, проверку качества данных и сроки подготовки регулярных отчётов.",
				URL:       "https://wiki.example/docs/reporting",
				CreatedAt: created(13),
			},
		},
		Program: &DatasetProgram{
			ID:    FixtureProgramID,
			Title: "Аналитика данных (демонстрационная программа)",
		},
		Competencies: []DatasetCompetency{
			{ID: "comp-sql", Code: "SQL", Title: "SQL и работа с хранилищем данных"},
			{ID: "comp-reporting", Code: "REP", Title: "Отчётность и визуализация"},
			{ID: "comp-quality", Code: "DQ", Title: "Качество данных"},
		},
		KSA: []DatasetKSA{
			{ID: "ksa-sql-queries", CompetencyID: "comp-sql", Kind: "skill", Title: "Писать SQL-запросы"},
			{ID: "ksa-data-model", CompetencyID: "comp-sql", Kind: "knowledge", Title: "Понимать модель данных хранилища"},
			{ID: "ksa-bi-dashboard", CompetencyID: "comp-reporting", Kind: "skill", Title: "Строить дашборды в BI"},
			{ID: "ksa-metrics", CompetencyID: "comp-reporting", Kind: "knowledge", Title: "Согласовывать методику расчёта метрик"},
			{ID: "ksa-quality-checks", CompetencyID: "comp-quality", Kind: "skill", Title: "Проверять выгрузки и качество данных"},
		},
		TaxonomyLinks: []DatasetTaxonomyLink{
			{CompetencyID: "comp-sql", TaxonomyItemID: "taxonomy.sql"},
			{KSAID: "ksa-bi-dashboard", TaxonomyItemID: "taxonomy.bi"},
			{KSAID: "ksa-quality-checks", TaxonomyItemID: "taxonomy.data-quality"},
		},
		AnalyzerMatches: []string{
			"src-fixture-vacancy-1",
			"src-fixture-vacancy-2",
			"src-fixture-interview-1",
			"src-fixture-interview-2",
			"src-fixture-standard-1",
			"src-fixture-document-1",
		},
	}
}

func validKSAKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "knowledge", "skill", "ability":
		return true
	default:
		return false
	}
}
