package ingestion_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"activity-descriptor/internal/ingestion"
)

func TestReadSkillSnapshotMatchesArchiveFiles(t *testing.T) {
	snapshot, err := ingestion.ReadSkillSnapshot(strings.NewReader(`{"Go": 15.5, "SQL": 0}`), "freq_market_2026-02.json")
	if err != nil || snapshot.Series != ingestion.MarketSkillSeries ||
		snapshot.Month.Format("2006-01-02") != "2026-02-01" || snapshot.Frequencies["Go"] != 15.5 {
		t.Fatalf("рыночный снимок: %+v, %v", snapshot, err)
	}
	snapshot, err = ingestion.ReadSkillSnapshot(strings.NewReader(
		`{"_meta":{"snapshot_date":"2026-03-14","profession":"Инженер"},"Go":20}`), "freq_market_2026-02.json")
	if err != nil || snapshot.Series != "profession_Инженер" || snapshot.Month.Format("2006-01-02") != "2026-03-01" {
		t.Fatalf("снимок профессии: %+v, %v", snapshot, err)
	}
	for _, input := range []string{
		`{"_meta":{"synthetic":true},"Go":100}`, `{"Go":null}`, `{"Go":-1}`,
		`{"Go":"42"}`, `{"Go":1e999}`, `{"\u0000":1}`, `{}`,
	} {
		if result, err := ingestion.ReadSkillSnapshot(strings.NewReader(input), "freq_market_2026-02.json"); err == nil {
			t.Errorf("%s принято: %+v", input, result)
		}
	}
}

func TestReadVacancyArchiveCombinesBasicAndDetailed(t *testing.T) {
	dir := t.TempDir()
	basic, detailed := filepath.Join(dir, "basic.json"), filepath.Join(dir, "detailed.json")
	if err := os.WriteFile(basic, []byte(`{"items":[{"id":"100","name":"Инженер","area":{"name":"Томск"},"employer":{"name":"Компания"},"salary":{"from":42,"currency":"RUR"}}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(detailed, []byte(`[{"id":"100","name":"Старший инженер","description":"Описание","employer":{"id":"7"},"key_skills":["Go"],"extracted_skills":["SQL"]},{"id":"200","name":"Аналитик"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	vacancies, err := ingestion.ReadVacancyArchive(basic, detailed)
	if err != nil || len(vacancies) != 2 {
		t.Fatalf("объединение: %+v, %v", vacancies, err)
	}
	item := vacancies[0]
	if item.Name != "Старший инженер" || item.Description != "Описание" || item.Area.Name != "Томск" ||
		item.Employer.Name != "Компания" || item.Employer.ID != "7" || item.Salary.From == nil ||
		*item.Salary.From != 42 || len(item.ExtractedSkills) != 1 || item.ExtractedSkills[0] != "SQL" {
		t.Fatalf("вакансия: %+v", item)
	}
}
