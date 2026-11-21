package pilot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// samplePath возвращает путь к общим примерам снимков в корне репозитория.
func samplePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "..", "testdata", "pilot", name)
}

// writeDataset сохраняет снимок во временный файл и возвращает путь.
func writeDataset(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dataset.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("записать снимок: %v", err)
	}
	return path
}

func TestLoadDatasetSourcesSample(t *testing.T) {
	dataset, err := LoadDataset(samplePath(t, "sources-sample.json"))
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	if len(dataset.Sources) != 6 {
		t.Fatalf("источников = %d, ожидалось 6", len(dataset.Sources))
	}
	if len(dataset.AnalyzerMatches) != 6 {
		t.Fatalf("совпадений анализатора = %d, ожидалось 6", len(dataset.AnalyzerMatches))
	}
	rows, err := dataset.SourcesByProfession(context.Background(), "Аналитик данных")
	if err != nil {
		t.Fatalf("SourcesByProfession: %v", err)
	}
	matched, err := dataset.MatchedSourceIDs(context.Background(), "Аналитик данных")
	if err != nil {
		t.Fatalf("MatchedSourceIDs: %v", err)
	}
	report := BuildSourceReport("Аналитик данных", rows, matched)
	if !report.Ready {
		t.Fatalf("пример источников должен быть готов: %#v", report.Problems)
	}
	if len(report.ByType) != 4 {
		t.Fatalf("типов = %d, ожидалось 4: %+v", len(report.ByType), report.ByType)
	}
	if report.AnalyzerMatches != 6 {
		t.Fatalf("совпадений анализатора в отчёте = %d, ожидалось 6", report.AnalyzerMatches)
	}
}

func TestLoadDatasetProgramSample(t *testing.T) {
	dataset, err := LoadDataset(samplePath(t, "program-sample.json"))
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	facts, err := dataset.LoadProgramFacts(context.Background(), "program-data-analyst-2026")
	if err != nil {
		t.Fatalf("LoadProgramFacts: %v", err)
	}
	if !facts.Found {
		t.Fatal("программа снимка не найдена")
	}
	if facts.Competencies != 3 || facts.KSA != 5 || facts.TaxonomyLinks != 3 {
		t.Fatalf("неверные счётчики программы: %+v", facts)
	}
	report := BuildProgramReport(facts)
	if !report.Ready {
		t.Fatalf("пример программы должен быть готов: %#v", report.Problems)
	}
}

func TestLoadDatasetRejectsBrokenFiles(t *testing.T) {
	t.Run("битый JSON", func(t *testing.T) {
		path := writeDataset(t, `{"sources": [`)
		_, err := LoadDataset(path)
		if err == nil || !strings.Contains(err.Error(), "разбор снимка данных") {
			t.Fatalf("ожидалась ошибка разбора, получено: %v", err)
		}
	})
	t.Run("неизвестное поле", func(t *testing.T) {
		path := writeDataset(t, `{"sources": [], "surprise": true}`)
		_, err := LoadDataset(path)
		if err == nil || !strings.Contains(err.Error(), "разбор снимка данных") {
			t.Fatalf("ожидалась ошибка неизвестного поля, получено: %v", err)
		}
	})
	t.Run("данные после JSON", func(t *testing.T) {
		path := writeDataset(t, `{"sources": []} {"sources": []}`)
		_, err := LoadDataset(path)
		if err == nil || !strings.Contains(err.Error(), "лишние данные") {
			t.Fatalf("ожидалась ошибка лишних данных, получено: %v", err)
		}
	})
	t.Run("файл отсутствует", func(t *testing.T) {
		_, err := LoadDataset(filepath.Join(t.TempDir(), "missing.json"))
		if err == nil || !strings.Contains(err.Error(), "чтение снимка") {
			t.Fatalf("ожидалась ошибка чтения, получено: %v", err)
		}
	})
}

func TestLoadDatasetAcceptsUTF8BOM(t *testing.T) {
	path := writeDataset(t, "\xEF\xBB\xBF"+`{"sources": [{"id": "s1", "type": "vacancy"}]}`)
	dataset, err := LoadDataset(path)
	if err != nil {
		t.Fatalf("снимок с UTF-8 BOM должен загружаться: %v", err)
	}
	if len(dataset.Sources) != 1 || dataset.Sources[0].ID != "s1" {
		t.Fatalf("снимок прочитан неверно: %+v", dataset)
	}
}

func TestDatasetValidation(t *testing.T) {
	tests := map[string]struct {
		document string
		want     string
	}{
		"источник без id": {
			document: `{"sources": [{"type": "vacancy"}]}`,
			want:     "без id",
		},
		"повтор источника": {
			document: `{"sources": [{"id": "s1"}, {"id": "S1"}]}`,
			want:     "повторяющийся id источника",
		},
		"совпадение с неизвестным источником": {
			document: `{"sources": [{"id": "s1"}], "analyzer_matches": ["ghost"]}`,
			want:     "неизвестный источник",
		},
		"компетенции без программы": {
			document: `{"competencies": [{"id": "c1"}]}`,
			want:     "нет программы",
		},
		"программа без названия": {
			document: `{"program": {"id": "p1", "title": " "}}`,
			want:     "пустое название",
		},
		"повтор компетенции": {
			document: `{"program": {"id": "p1", "title": "П"}, "competencies": [{"id": "c1"}, {"id": "c1"}]}`,
			want:     "повторяющийся id компетенции",
		},
		"KSA без компетенции": {
			document: `{"program": {"id": "p1", "title": "П"}, "ksa": [{"id": "k1", "competency_id": "ghost"}]}`,
			want:     "неизвестную компетенцию",
		},
		"неизвестный kind": {
			document: `{"program": {"id": "p1", "title": "П"}, "ksa": [{"id": "k1", "kind": "habit"}]}`,
			want:     "неизвестный kind",
		},
		"связь без цели": {
			document: `{"program": {"id": "p1", "title": "П"}, "taxonomy_links": [{"taxonomy_item_id": "t1"}]}`,
			want:     "ни competency_id, ни ksa_id",
		},
		"связь с двумя целями": {
			document: `{"program": {"id": "p1", "title": "П"}, "competencies": [{"id": "c1"}], "ksa": [{"id": "k1"}], "taxonomy_links": [{"competency_id": "c1", "ksa_id": "k1", "taxonomy_item_id": "t1"}]}`,
			want:     "одновременно",
		},
		"связь с неизвестным KSA": {
			document: `{"program": {"id": "p1", "title": "П"}, "taxonomy_links": [{"ksa_id": "ghost", "taxonomy_item_id": "t1"}]}`,
			want:     "неизвестный KSA",
		},
		"связь без taxonomy_item_id": {
			document: `{"program": {"id": "p1", "title": "П"}, "competencies": [{"id": "c1"}], "taxonomy_links": [{"competency_id": "c1"}]}`,
			want:     "без taxonomy_item_id",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := LoadDataset(writeDataset(t, test.document))
			if err == nil {
				t.Fatal("ожидалась ошибка валидации")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ошибка %q не содержит %q", err, test.want)
			}
		})
	}
}

func TestFixtureГотовКАудиту(t *testing.T) {
	dataset := Fixture()
	if err := dataset.Validate(); err != nil {
		t.Fatalf("встроенный набор не проходит валидацию: %v", err)
	}

	rows, err := dataset.SourcesByProfession(context.Background(), FixtureProfession)
	if err != nil {
		t.Fatalf("SourcesByProfession: %v", err)
	}
	matched, err := dataset.MatchedSourceIDs(context.Background(), FixtureProfession)
	if err != nil {
		t.Fatalf("MatchedSourceIDs: %v", err)
	}
	if len(rows) != 6 {
		t.Fatalf("источников = %d, ожидалось 6", len(rows))
	}
	sourceReport := BuildSourceReport(FixtureProfession, rows, matched)
	if !sourceReport.Ready {
		t.Fatalf("источники фикстуры должны быть готовы: %#v", sourceReport.Problems)
	}
	if len(sourceReport.Problems) != 0 {
		t.Fatalf("у фикстуры не должно быть замечаний по источникам: %#v", sourceReport.Problems)
	}
	if len(sourceReport.ByType) != 4 {
		t.Fatalf("типов = %d, ожидалось 4: %+v", len(sourceReport.ByType), sourceReport.ByType)
	}

	facts, err := dataset.LoadProgramFacts(context.Background(), FixtureProgramID)
	if err != nil {
		t.Fatalf("LoadProgramFacts: %v", err)
	}
	if !facts.Found || facts.Competencies != 3 || facts.KSA != 5 || facts.TaxonomyLinks != 3 {
		t.Fatalf("неверные факты фикстуры: %+v", facts)
	}
	programReport := BuildProgramReport(facts)
	if !programReport.Ready {
		t.Fatalf("программа фикстуры должна быть готова: %#v", programReport.Problems)
	}
}

func TestПустойСнимокТребуетВнимания(t *testing.T) {
	dataset := &Dataset{}
	if err := dataset.Validate(); err != nil {
		t.Fatalf("пустой снимок должен загружаться: %v", err)
	}

	rows, err := dataset.SourcesByProfession(context.Background(), "Аналитик данных")
	if err != nil {
		t.Fatalf("SourcesByProfession: %v", err)
	}
	matched, err := dataset.MatchedSourceIDs(context.Background(), "Аналитик данных")
	if err != nil {
		t.Fatalf("MatchedSourceIDs: %v", err)
	}
	sourceReport := BuildSourceReport("Аналитик данных", rows, matched)
	if sourceReport.Ready {
		t.Fatal("пустой снимок не может быть готов")
	}
	if text := FormatSourceReport(sourceReport); !strings.Contains(text, "Статус: требует внимания") {
		t.Fatalf("в отчёте нет статуса «требует внимания»:\n%s", text)
	}

	facts, err := dataset.LoadProgramFacts(context.Background(), "program-missing")
	if err != nil {
		t.Fatalf("LoadProgramFacts: %v", err)
	}
	programReport := BuildProgramReport(facts)
	if programReport.Ready {
		t.Fatal("отсутствующая программа не может быть готова")
	}
	if text := FormatProgramReport(programReport); !strings.Contains(text, "Статус: требует внимания") {
		t.Fatalf("в отчёте программы нет статуса «требует внимания»:\n%s", text)
	}
}

func TestDatasetИщетТолькоСвоюПрограмму(t *testing.T) {
	dataset := Fixture()
	facts, err := dataset.LoadProgramFacts(context.Background(), "program-other")
	if err != nil {
		t.Fatalf("LoadProgramFacts: %v", err)
	}
	if facts.Found {
		t.Fatalf("чужая программа найдена: %+v", facts)
	}

	if _, err := dataset.SourcesByProfession(context.Background(), "  "); err == nil {
		t.Fatal("пустая профессия должна давать ошибку")
	}
	if _, err := dataset.MatchedSourceIDs(context.Background(), ""); err == nil {
		t.Fatal("пустая профессия должна давать ошибку")
	}
	if _, err := dataset.LoadProgramFacts(context.Background(), " "); err == nil {
		t.Fatal("пустой ID программы должен давать ошибку")
	}
}

func TestFixtureВозвращаетКопию(t *testing.T) {
	first := Fixture()
	first.Sources[0].ID = "changed"
	first.TaxonomyLinks[0].TaxonomyItemID = "changed"
	second := Fixture()
	if second.Sources[0].ID == "changed" || second.TaxonomyLinks[0].TaxonomyItemID == "changed" {
		t.Fatal("Fixture должен возвращать независимую копию данных")
	}
}
