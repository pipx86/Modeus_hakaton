package pilot

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var testDate = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func testSource(id, sourceType, content, url string) SourceRow {
	return SourceRow{
		ID:        id,
		Type:      sourceType,
		Title:     "Источник " + id,
		Content:   content,
		URL:       url,
		CreatedAt: testDate,
	}
}

func auditByType(t *testing.T, report SourceReport, sourceType string) SourceAudit {
	t.Helper()
	for _, item := range report.ByType {
		if item.Type == sourceType {
			return item
		}
	}
	t.Fatalf("в отчёте нет типа %q: %+v", sourceType, report.ByType)
	return SourceAudit{}
}

func problemsContain(problems []string, substring string) bool {
	for _, problem := range problems {
		if strings.Contains(problem, substring) {
			return true
		}
	}
	return false
}

func TestПустойНаборИсточников(t *testing.T) {
	report := BuildSourceReport("  аналитик данных  ", nil, nil)

	if report.Profession != "аналитик данных" {
		t.Fatalf("профессия не нормализована: %q", report.Profession)
	}
	if report.Ready {
		t.Fatal("пустой набор не может быть готов")
	}
	if report.Total != 0 || report.AnalyzerMatches != 0 {
		t.Fatalf("неверные счётчики: %+v", report)
	}
	if report.ByType == nil || len(report.ByType) != 0 {
		t.Fatalf("ByType должен быть пустым массивом, получено: %#v", report.ByType)
	}
	if report.Problems == nil || len(report.Problems) != 3 {
		t.Fatalf("ожидались три проблемы, получено: %#v", report.Problems)
	}
	for _, want := range []string{"источников меньше 5: 0", "типов источников меньше 3: 0", "нет ни одного источника"} {
		if !problemsContain(report.Problems, want) {
			t.Fatalf("нет проблемы %q в %#v", want, report.Problems)
		}
	}
}

func TestРовноПорогГотов(t *testing.T) {
	rows := []SourceRow{
		testSource("s1", "vacancy", "текст вакансии", "https://example.org/1"),
		testSource("s2", "vacancy", "ещё текст", "https://example.org/2"),
		testSource("s3", "interview", "ответ респондента", "https://example.org/3"),
		testSource("s4", "document", "профстандарт", "https://example.org/4"),
		testSource("s5", "document", "описание практики", "https://example.org/5"),
	}
	report := BuildSourceReport("аналитик данных", rows, []string{"s1", "s1", "s2", "чужой-id"})

	if !report.Ready {
		t.Fatalf("порог должен давать готовность: %+v", report.Problems)
	}
	if report.Total != 5 || report.AnalyzerMatches != 2 {
		t.Fatalf("неверные счётчики: %+v", report)
	}
	if len(report.Problems) != 0 {
		t.Fatalf("у готового набора не должно быть проблем: %#v", report.Problems)
	}
	if len(report.ByType) != 3 {
		t.Fatalf("ожидались три типа: %+v", report.ByType)
	}
	if item := auditByType(t, report, "vacancy"); item.Total != 2 || item.WithContent != 2 || item.WithURL != 2 || item.Dated != 2 || item.Valid != 2 {
		t.Fatalf("неверное покрытие vacancy: %+v", item)
	}
	if item := auditByType(t, report, "document"); item.Total != 2 || item.Valid != 2 {
		t.Fatalf("неверное покрытие document: %+v", item)
	}
}

func TestРазныеТипыИЧастичноеПокрытие(t *testing.T) {
	rows := []SourceRow{
		{ID: "v1", Type: "vacancy", Title: "Вакансия без ссылки", Content: "текст", CreatedAt: testDate},
		{ID: "v2", Type: "vacancy", Title: "Вакансия без текста", URL: "https://example.org/v2", PublishedAt: testDate},
		testSource("d1", "document", "документ", "https://example.org/d1"),
		testSource("d2", "document", "документ", "https://example.org/d2"),
		{ID: "i1", Type: "interview", Title: "Интервью", Content: "текст", URL: "https://example.org/i1"},
	}
	report := BuildSourceReport("аналитик данных", rows, nil)

	if !report.Ready {
		t.Fatalf("набор с тремя типами и пригодными источниками должен быть готов: %#v", report.Problems)
	}
	if report.Total != 5 || report.AnalyzerMatches != 0 {
		t.Fatalf("неверные счётчики: %+v", report)
	}
	if item := auditByType(t, report, "document"); item.Total != 2 || item.WithContent != 2 || item.WithURL != 2 || item.Dated != 2 || item.Valid != 2 {
		t.Fatalf("неверное покрытие document: %+v", item)
	}
	if item := auditByType(t, report, "interview"); item.Total != 1 || item.WithContent != 1 || item.WithURL != 1 || item.Dated != 0 || item.Valid != 1 {
		t.Fatalf("неверное покрытие interview: %+v", item)
	}
	if item := auditByType(t, report, "vacancy"); item.Total != 2 || item.WithContent != 1 || item.WithURL != 1 || item.Dated != 2 || item.Valid != 0 {
		t.Fatalf("неверное покрытие vacancy: %+v", item)
	}
	for _, want := range []string{"у типа vacancy нет URL у 1 из 2", "у типа vacancy нет текста у 1 из 2", "анализатор не нашёл"} {
		if !problemsContain(report.Problems, want) {
			t.Fatalf("нет проблемы %q в %#v", want, report.Problems)
		}
	}
}

func TestНетURL(t *testing.T) {
	rows := []SourceRow{
		testSource("s1", "vacancy", "текст", ""),
		testSource("s2", "vacancy", "текст", ""),
		testSource("s3", "interview", "текст", ""),
		testSource("s4", "document", "текст", ""),
		testSource("s5", "document", "текст", ""),
	}
	report := BuildSourceReport("аналитик", rows, []string{"s1"})

	if report.Ready {
		t.Fatal("без URL набор не готов")
	}
	for _, want := range []string{"нет URL", "одновременно с URL и непустым текстом"} {
		if !problemsContain(report.Problems, want) {
			t.Fatalf("нет проблемы %q в %#v", want, report.Problems)
		}
	}
}

func TestНетТекста(t *testing.T) {
	rows := []SourceRow{
		testSource("s1", "vacancy", "", "https://example.org/1"),
		testSource("s2", "vacancy", "  ", "https://example.org/2"),
		testSource("s3", "interview", "", "https://example.org/3"),
		testSource("s4", "document", "", "https://example.org/4"),
		testSource("s5", "document", "", "https://example.org/5"),
	}
	report := BuildSourceReport("аналитик", rows, []string{"s1"})

	if report.Ready {
		t.Fatal("без текста набор не готов")
	}
	for _, want := range []string{"нет текста", "одновременно с URL и непустым текстом"} {
		if !problemsContain(report.Problems, want) {
			t.Fatalf("нет проблемы %q в %#v", want, report.Problems)
		}
	}
}

func TestМалоТипов(t *testing.T) {
	rows := []SourceRow{
		testSource("s1", "vacancy", "текст", "https://example.org/1"),
		testSource("s2", "vacancy", "текст", "https://example.org/2"),
		testSource("s3", "vacancy", "текст", "https://example.org/3"),
		testSource("s4", "document", "текст", "https://example.org/4"),
		testSource("s5", "document", "текст", "https://example.org/5"),
	}
	report := BuildSourceReport("аналитик", rows, []string{"s1"})

	if report.Ready {
		t.Fatal("двух типов недостаточно")
	}
	if !problemsContain(report.Problems, "типов источников меньше 3: 2") {
		t.Fatalf("нет проблемы малого числа типов: %#v", report.Problems)
	}
}

func TestИсточникБезТипа(t *testing.T) {
	rows := []SourceRow{
		testSource("s1", "vacancy", "текст", "https://example.org/1"),
		testSource("s2", "interview", "текст", "https://example.org/2"),
		testSource("s3", "document", "текст", "https://example.org/3"),
		testSource("s4", "", "текст", "https://example.org/4"),
		testSource("s5", "document", "текст", "https://example.org/5"),
	}
	report := BuildSourceReport("аналитик", rows, []string{"s1"})

	if !report.Ready {
		t.Fatalf("три валидных типа и порог выполнены: %#v", report.Problems)
	}
	if item := auditByType(t, report, unknownSourceType); item.Total != 1 {
		t.Fatalf("источник без типа не учтён: %+v", report.ByType)
	}
	if !problemsContain(report.Problems, "без типа: 1") {
		t.Fatalf("нет проблемы источника без типа: %#v", report.Problems)
	}
}

func TestПрограммаБезKSA(t *testing.T) {
	report := BuildProgramReport(ProgramFacts{
		ID:                  "program-1",
		Title:               "Аналитик данных",
		Found:               true,
		Competencies:        3,
		KSA:                 0,
		TaxonomyLinks:       2,
		HasLearningOutcomes: true,
		HasDisciplines:      true,
	})

	if report.Ready {
		t.Fatal("программа без KSA не готова")
	}
	if !problemsContain(report.Problems, "KSA программы не загружены") {
		t.Fatalf("нет проблемы отсутствия KSA: %#v", report.Problems)
	}
	if report.ProgramID != "program-1" || report.Competencies != 3 || report.KSA != 0 || report.TaxonomyLinks != 2 {
		t.Fatalf("счётчики не перенесены в отчёт: %+v", report)
	}
}

func TestПрограммаПолностьюГотова(t *testing.T) {
	report := BuildProgramReport(ProgramFacts{
		ID:                  "program-1",
		Title:               "Аналитик данных",
		Found:               true,
		Competencies:        3,
		KSA:                 5,
		TaxonomyLinks:       1,
		HasLearningOutcomes: true,
		HasDisciplines:      true,
	})

	if !report.Ready {
		t.Fatalf("программа должна быть готова: %#v", report.Problems)
	}
	if len(report.Problems) != 0 {
		t.Fatalf("у готовой программы не должно быть проблем: %#v", report.Problems)
	}
}

func TestПрограммаГотоваБезДисциплинИРезультатов(t *testing.T) {
	report := BuildProgramReport(ProgramFacts{
		ID:            "program-1",
		Title:         "Аналитик данных",
		Found:         true,
		Competencies:  3,
		KSA:           5,
		TaxonomyLinks: 1,
	})

	if !report.Ready {
		t.Fatal("дисциплины и результаты обучения не входят в готовность MVP")
	}
	for _, want := range []string{"результаты обучения не представлены структурно", "дисциплины не представлены структурно"} {
		if !problemsContain(report.Problems, want) {
			t.Fatalf("нет ограничения %q в %#v", want, report.Problems)
		}
	}
}

func TestПрограммаНижеПорогов(t *testing.T) {
	report := BuildProgramReport(ProgramFacts{
		ID:                  "program-1",
		Title:               "Аналитик данных",
		Found:               true,
		Competencies:        2,
		KSA:                 4,
		TaxonomyLinks:       0,
		HasLearningOutcomes: true,
		HasDisciplines:      true,
	})

	if report.Ready {
		t.Fatal("программа ниже порогов не готова")
	}
	for _, want := range []string{"компетенций меньше 3: 2", "KSA меньше 5: 4", "нет связей компетенций и KSA"} {
		if !problemsContain(report.Problems, want) {
			t.Fatalf("нет проблемы %q в %#v", want, report.Problems)
		}
	}
}

func TestПрограммаНеНайдена(t *testing.T) {
	report := BuildProgramReport(ProgramFacts{ID: "missing-program"})

	if report.Ready {
		t.Fatal("ненайденная программа не готова")
	}
	if !problemsContain(report.Problems, "не найдена в базе") {
		t.Fatalf("нет проблемы отсутствия программы: %#v", report.Problems)
	}
}

func TestСериализацияВJSON(t *testing.T) {
	sourceReport := SourceReport{
		Profession:      "аналитик данных",
		Total:           0,
		ByType:          []SourceAudit{},
		AnalyzerMatches: 0,
		Problems:        []string{},
		Ready:           false,
	}
	data, err := ToJSON(sourceReport)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Fatal("JSON должен заканчиваться переводом строки")
	}
	for _, key := range []string{`"profession"`, `"total"`, `"by_type"`, `"analyzer_matches"`, `"problems"`, `"ready"`} {
		if !strings.Contains(string(data), key) {
			t.Fatalf("в JSON нет ключа %s:\n%s", key, data)
		}
	}
	if !strings.Contains(string(data), `"by_type": []`) || !strings.Contains(string(data), `"problems": []`) {
		t.Fatalf("пустые срезы должны быть массивами, а не null:\n%s", data)
	}
	var decodedSource SourceReport
	if err := json.Unmarshal(data, &decodedSource); err != nil {
		t.Fatal(err)
	}
	if decodedSource.Profession != sourceReport.Profession || decodedSource.Ready != sourceReport.Ready {
		t.Fatalf("JSON не восстанавливается: %+v", decodedSource)
	}

	programReport := ProgramAudit{
		ProgramID:           "program-1",
		Title:               "Аналитик данных",
		Competencies:        3,
		KSA:                 5,
		TaxonomyLinks:       2,
		HasLearningOutcomes: false,
		HasDisciplines:      false,
		Problems:            []string{"дисциплины не представлены структурно"},
		Ready:               true,
	}
	programData, err := ToJSON(programReport)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"program_id"`, `"ksa"`, `"taxonomy_links"`, `"has_learning_outcomes"`, `"has_disciplines"`, `"ready"`} {
		if !strings.Contains(string(programData), key) {
			t.Fatalf("в JSON программы нет ключа %s:\n%s", key, programData)
		}
	}
	var decodedProgram ProgramAudit
	if err := json.Unmarshal(programData, &decodedProgram); err != nil {
		t.Fatal(err)
	}
	if decodedProgram.ProgramID != programReport.ProgramID || !decodedProgram.Ready || decodedProgram.KSA != 5 {
		t.Fatalf("JSON программы не восстанавливается: %+v", decodedProgram)
	}
}

func TestТекстовыйОтчёт(t *testing.T) {
	sourceText := FormatSourceReport(SourceReport{
		Profession: "аналитик данных",
		Total:      5,
		ByType: []SourceAudit{
			{Type: "document", Total: 2, WithContent: 2, WithURL: 2, Dated: 2, Valid: 2},
		},
		AnalyzerMatches: 1,
		Problems:        []string{},
		Ready:           true,
	})
	for _, want := range []string{"Аудит источников", "Статус: готово", "Всего источников: 5", "Проблемы: нет"} {
		if !strings.Contains(sourceText, want) {
			t.Fatalf("в тексте нет %q:\n%s", want, sourceText)
		}
	}

	programText := FormatProgramReport(ProgramAudit{
		ProgramID: "program-1",
		Title:     "Аналитик данных",
		Problems:  []string{"KSA программы не загружены"},
		Ready:     false,
	})
	for _, want := range []string{"Аудит программы", "Статус: требует внимания", "Результаты обучения: только в документах", "  - KSA программы не загружены"} {
		if !strings.Contains(programText, want) {
			t.Fatalf("в тексте нет %q:\n%s", want, programText)
		}
	}
}
