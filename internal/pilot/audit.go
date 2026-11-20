// Package pilot проверяет готовность данных пилота: источники по профессии
// (карточка 4) и структурированные данные образовательной программы
// (карточка 5). Пакет только читает и интерпретирует данные: импорт, схему и
// frontend он не меняет.
package pilot

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Пороги готовности. Они взяты из чек-листа карточки 2 плана MVP и вынесены
// в константы, чтобы отчёт и README не расходились.
const (
	// MinSourceTotal — минимум источников по пилотной профессии.
	MinSourceTotal = 5
	// MinSourceTypes — минимум разных типов источников.
	MinSourceTypes = 3
	// MinProgramCompetencies — минимум загруженных компетенций программы.
	MinProgramCompetencies = 3
	// MinProgramKSA — минимум загруженных KSA программы.
	MinProgramKSA = 5
)

// unknownSourceType используется в отчёте, если у источника пустой type.
const unknownSourceType = "unknown"

// SourceRow — сведения об источнике, которые нужны аудиту. Даты могут быть
// нулевыми: схема хранит created_at (дату импорта) и, только для вакансий,
// vacancy_published_at; отдельной даты материала у остальных типов нет.
type SourceRow struct {
	ID          string
	Type        string
	Title       string
	Content     string
	URL         string
	CreatedAt   time.Time
	PublishedAt time.Time
}

// SourceAudit — покрытие одного типа источников. Valid означает, что у
// источника одновременно есть непустой текст и URL; дата учитывается отдельно
// в Dated и не входит в Valid.
type SourceAudit struct {
	Type        string `json:"type"`
	Total       int    `json:"total"`
	WithContent int    `json:"with_content"`
	WithURL     int    `json:"with_url"`
	Dated       int    `json:"dated"`
	Valid       int    `json:"valid"`
}

// SourceReport — результат аудита источников по профессии. Ready отражает
// только правила готовности; Problems перечисляет все найденные замечания,
// включая не блокирующие готовность (например, нулевое совпадение с
// анализатором).
type SourceReport struct {
	Profession      string        `json:"profession"`
	Total           int           `json:"total"`
	ByType          []SourceAudit `json:"by_type"`
	AnalyzerMatches int           `json:"analyzer_matches"`
	Problems        []string      `json:"problems"`
	Ready           bool          `json:"ready"`
}

// ProgramFacts — всё, что аудит знает о программе до применения правил.
// Found=false означает, что программы с таким ID нет в базе.
type ProgramFacts struct {
	ID                  string
	Title               string
	Found               bool
	Competencies        int
	KSA                 int
	TaxonomyLinks       int
	HasLearningOutcomes bool
	HasDisciplines      bool
}

// ProgramAudit — результат аудита выбранной образовательной программы.
// HasLearningOutcomes и HasDisciplines показывают, есть ли для них
// структурированные данные в схеме; в текущей схеме таких таблиц нет, поэтому
// значения остаются false, а отчёт честно говорит «только в документах».
type ProgramAudit struct {
	ProgramID           string   `json:"program_id"`
	Title               string   `json:"title"`
	Competencies        int      `json:"competencies"`
	KSA                 int      `json:"ksa"`
	TaxonomyLinks       int      `json:"taxonomy_links"`
	HasLearningOutcomes bool     `json:"has_learning_outcomes"`
	HasDisciplines      bool     `json:"has_disciplines"`
	Problems            []string `json:"problems"`
	Ready               bool     `json:"ready"`
}

// BuildSourceReport применяет правила готовности к уже прочитанным источникам:
// не меньше MinSourceTotal источников, не меньше MinSourceTypes типов и хотя бы
// один источник одновременно с непустым текстом и URL. matchedIDs — ID
// источников, которые нашёл анализатор; учитываются только ID, присутствующие
// в rows, без повторов.
func BuildSourceReport(profession string, rows []SourceRow, matchedIDs []string) SourceReport {
	report := SourceReport{
		Profession: strings.TrimSpace(profession),
		Total:      len(rows),
		ByType:     make([]SourceAudit, 0),
		Problems:   make([]string, 0),
	}

	typeIndex := make(map[string]int)
	distinctTypes := make(map[string]struct{})
	blankTypes := 0
	validRows := 0
	for _, row := range rows {
		sourceType := normalizeSourceType(row.Type)
		if sourceType == unknownSourceType {
			blankTypes++
		} else {
			distinctTypes[sourceType] = struct{}{}
		}

		index, ok := typeIndex[sourceType]
		if !ok {
			index = len(report.ByType)
			typeIndex[sourceType] = index
			report.ByType = append(report.ByType, SourceAudit{Type: sourceType})
		}
		item := &report.ByType[index]
		item.Total++
		if hasText(row.Content) {
			item.WithContent++
		}
		if hasText(row.URL) {
			item.WithURL++
		}
		if !row.CreatedAt.IsZero() || !row.PublishedAt.IsZero() {
			item.Dated++
		}
		if hasText(row.Content) && hasText(row.URL) {
			item.Valid++
			validRows++
		}
	}

	// Порядок типов фиксирован, чтобы текстовый и JSON-отчёты не зависели от
	// порядка строк в базе.
	sort.Slice(report.ByType, func(i, j int) bool { return report.ByType[i].Type < report.ByType[j].Type })
	report.AnalyzerMatches = countAnalyzerMatches(matchedIDs, rows)

	if report.Total < MinSourceTotal {
		report.Problems = append(report.Problems, fmt.Sprintf("источников меньше %d: %d", MinSourceTotal, report.Total))
	}
	if len(distinctTypes) < MinSourceTypes {
		report.Problems = append(report.Problems, fmt.Sprintf("типов источников меньше %d: %d", MinSourceTypes, len(distinctTypes)))
	}
	if validRows == 0 {
		report.Problems = append(report.Problems, "нет ни одного источника одновременно с URL и непустым текстом")
	}
	if blankTypes > 0 {
		report.Problems = append(report.Problems, fmt.Sprintf("есть источники без типа: %d", blankTypes))
	}
	for _, item := range report.ByType {
		if item.WithURL < item.Total {
			report.Problems = append(report.Problems, fmt.Sprintf("у типа %s нет URL у %d из %d источников", displaySourceType(item.Type), item.Total-item.WithURL, item.Total))
		}
		if item.WithContent < item.Total {
			report.Problems = append(report.Problems, fmt.Sprintf("у типа %s нет текста у %d из %d источников", displaySourceType(item.Type), item.Total-item.WithContent, item.Total))
		}
	}
	if report.Total > 0 && report.AnalyzerMatches == 0 {
		report.Problems = append(report.Problems, "анализатор не нашёл ни одного источника по профессии")
	}

	report.Ready = report.Total >= MinSourceTotal &&
		len(distinctTypes) >= MinSourceTypes &&
		validRows > 0
	return report
}

// BuildProgramReport применяет правила готовности программы: программа найдена,
// загружено не меньше MinProgramCompetencies компетенций, не меньше
// MinProgramKSA KSA и есть хотя бы одна связь компетенции или KSA с таксономией.
// Отсутствие дисциплин и результатов обучения не блокирует готовность MVP: по
// плану они не сопоставляются структурно, но попадают в Problems как
// ограничение.
func BuildProgramReport(facts ProgramFacts) ProgramAudit {
	report := ProgramAudit{
		ProgramID:           strings.TrimSpace(facts.ID),
		Title:               strings.TrimSpace(facts.Title),
		Competencies:        facts.Competencies,
		KSA:                 facts.KSA,
		TaxonomyLinks:       facts.TaxonomyLinks,
		HasLearningOutcomes: facts.HasLearningOutcomes,
		HasDisciplines:      facts.HasDisciplines,
		Problems:            make([]string, 0),
	}

	if !facts.Found {
		report.Problems = append(report.Problems, "программа с указанным ID не найдена в базе")
		return report
	}
	if facts.Competencies == 0 {
		report.Problems = append(report.Problems, "компетенции программы не загружены")
	} else if facts.Competencies < MinProgramCompetencies {
		report.Problems = append(report.Problems, fmt.Sprintf("компетенций меньше %d: %d", MinProgramCompetencies, facts.Competencies))
	}
	if facts.KSA == 0 {
		report.Problems = append(report.Problems, "KSA программы не загружены")
	} else if facts.KSA < MinProgramKSA {
		report.Problems = append(report.Problems, fmt.Sprintf("KSA меньше %d: %d", MinProgramKSA, facts.KSA))
	}
	if facts.TaxonomyLinks == 0 {
		report.Problems = append(report.Problems, "нет связей компетенций и KSA с таксономией")
	}
	if !facts.HasLearningOutcomes {
		report.Problems = append(report.Problems, "результаты обучения не представлены структурно — только в документах")
	}
	if !facts.HasDisciplines {
		report.Problems = append(report.Problems, "дисциплины не представлены структурно — только в документах")
	}

	report.Ready = facts.Competencies >= MinProgramCompetencies &&
		facts.KSA >= MinProgramKSA &&
		facts.TaxonomyLinks > 0
	return report
}

// FormatSourceReport возвращает компактный текстовый отчёт для человека.
// Статус показывает готовность по правилам, а раздел «Проблемы» — все
// замечания, в том числе не блокирующие.
func FormatSourceReport(report SourceReport) string {
	var builder strings.Builder
	profession := report.Profession
	if profession == "" {
		profession = "не задана"
	}
	fmt.Fprintf(&builder, "Аудит источников по профессии «%s»\n", profession)
	fmt.Fprintf(&builder, "Статус: %s\n", readinessLabel(report.Ready))
	fmt.Fprintf(&builder, "Всего источников: %d, найдено анализатором: %d\n", report.Total, report.AnalyzerMatches)
	if len(report.ByType) == 0 {
		builder.WriteString("По типам: данных нет\n")
	} else {
		builder.WriteString("По типам (всего / с текстом / с URL / с датой / пригодных):\n")
		for _, item := range report.ByType {
			fmt.Fprintf(&builder, "  - %s: %d / %d / %d / %d / %d\n",
				displaySourceType(item.Type), item.Total, item.WithContent, item.WithURL, item.Dated, item.Valid)
		}
	}
	formatProblems(&builder, report.Problems)
	return builder.String()
}

// FormatProgramReport возвращает компактный текстовый отчёт по программе.
func FormatProgramReport(report ProgramAudit) string {
	var builder strings.Builder
	title := "не найдена"
	if report.Title != "" {
		title = "«" + report.Title + "»"
	}
	programID := report.ProgramID
	if programID == "" {
		programID = "не указан"
	}
	fmt.Fprintf(&builder, "Аудит программы: %s (ID: %s)\n", title, programID)
	fmt.Fprintf(&builder, "Статус: %s\n", readinessLabel(report.Ready))
	fmt.Fprintf(&builder, "Компетенции: %d, KSA: %d, связи с таксономией: %d\n",
		report.Competencies, report.KSA, report.TaxonomyLinks)
	fmt.Fprintf(&builder, "Результаты обучения: %s\n", structuredLabel(report.HasLearningOutcomes))
	fmt.Fprintf(&builder, "Дисциплины: %s\n", structuredLabel(report.HasDisciplines))
	formatProblems(&builder, report.Problems)
	return builder.String()
}

// ToJSON сериализует отчёт в читаемый JSON и добавляет перевод строки, чтобы
// вывод команды было удобно направлять в файл.
func ToJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("сериализовать отчёт в JSON: %w", err)
	}
	return append(data, '\n'), nil
}

// normalizeSourceType приводит тип к нижнему регистру и заменяет пустое
// значение, чтобы отчёт не терял строки с повреждённым type.
func normalizeSourceType(value string) string {
	sourceType := strings.ToLower(strings.TrimSpace(value))
	if sourceType == "" {
		return unknownSourceType
	}
	return sourceType
}

func displaySourceType(sourceType string) string {
	if sourceType == unknownSourceType {
		return "без типа"
	}
	return sourceType
}

// countAnalyzerMatches считает уникальные ID анализатора, которые есть среди
// прочитанных источников. ID сравниваются без учёта регистра и пробелов.
func countAnalyzerMatches(matchedIDs []string, rows []SourceRow) int {
	sourceIDs := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if id := normalizeID(row.ID); id != "" {
			sourceIDs[id] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(matchedIDs))
	for _, id := range matchedIDs {
		id = normalizeID(id)
		if id == "" {
			continue
		}
		if _, ok := sourceIDs[id]; !ok {
			continue
		}
		seen[id] = struct{}{}
	}
	return len(seen)
}

func normalizeID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func hasText(value string) bool {
	return strings.TrimSpace(value) != ""
}

func readinessLabel(ready bool) string {
	if ready {
		return "готово"
	}
	return "требует внимания"
}

func structuredLabel(present bool) string {
	if present {
		return "структурированы"
	}
	return "только в документах"
}

func formatProblems(builder *strings.Builder, problems []string) {
	if len(problems) == 0 {
		builder.WriteString("Проблемы: нет\n")
		return
	}
	builder.WriteString("Проблемы:\n")
	for _, problem := range problems {
		fmt.Fprintf(builder, "  - %s\n", problem)
	}
}
