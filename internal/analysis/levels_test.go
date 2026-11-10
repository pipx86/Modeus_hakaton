package analysis

import "testing"

func TestAssignLevelsSkipsEmptySkills(t *testing.T) {
	vacs := []VacancyInput{
		{Name: "Python Dev", Skills: nil},
		{Name: "Java Dev", Skills: []string{"java"}},
	}
	res := AssignLevels(vacs, []string{"desc1", "desc2"})
	if len(res.Levels) != 1 || len(res.Skills) != 1 {
		t.Fatalf("ожидали 1 запись после фильтрации пустых, получили %d/%d", len(res.Levels), len(res.Skills))
	}
}

func TestClassifyExperienceHHEnumIDs(t *testing.T) {
	cases := map[string]ExperienceLevel{
		"no_experience":          LevelJunior,
		"less_than_1_year":       LevelJunior,
		"between_1_and_3_years":  LevelMiddle,
		"between_3_and_6_years":  LevelMiddle,
		"between_6_and_10_years": LevelSenior,
		"over_10_years":          LevelSenior,
		"":                       LevelMiddle,
	}
	for input, want := range cases {
		got := classifyExperience(input)
		if got != want {
			t.Fatalf("classifyExperience(%q) = %s, ожидали %s", input, got, want)
		}
	}
}

func TestClassifyExperienceFreeTextRuEn(t *testing.T) {
	cases := map[string]ExperienceLevel{
		"Junior Developer": LevelJunior,
		"Нет опыта работы": LevelJunior,
		"Стажёр в команде": LevelJunior,
		"Senior Engineer":  LevelSenior,
		"Опыт более 6 лет": LevelSenior,
		"Some random text": LevelMiddle,
	}
	for input, want := range cases {
		got := classifyExperience(input)
		if got != want {
			t.Fatalf("classifyExperience(%q) = %s, ожидали %s", input, got, want)
		}
	}
}

func TestNameOverridesDefaultMiddle(t *testing.T) {
	vacs := []VacancyInput{
		{Name: "Junior Python Developer", Skills: []string{"python"}, Experience: ""},
		{Name: "Ведущий Java архитектор", Skills: []string{"java"}, Experience: ""},
		{Name: "Intern QA", Skills: []string{"testing"}, Experience: ""},
		{Name: "Backend Engineer", Skills: []string{"go"}, Experience: ""},
	}
	res := AssignLevels(vacs, nil)
	wantExp := []ExperienceLevel{LevelJunior, LevelSenior, LevelJunior, LevelMiddle}
	for i, w := range wantExp {
		if res.Levels[i].Experience != w {
			t.Fatalf("вакансия %d (%s): ожидали %s, получили %s", i, vacs[i].Name, w, res.Levels[i].Experience)
		}
	}
}

func TestEnumTakesPriorityOverName(t *testing.T) {
	vacs := []VacancyInput{
		{Name: "Junior-looking title", Skills: []string{"x"}, Experience: "more_than_10_years"},
	}
	res := AssignLevels(vacs, nil)
	if res.Levels[0].Experience != LevelSenior {
		t.Fatalf("enum должен иметь приоритет над именем: получили %s", res.Levels[0].Experience)
	}
}

func TestDescriptionAlignmentByIndex(t *testing.T) {
	vacs := []VacancyInput{
		{Name: "A", Skills: []string{"sa"}, Experience: "junior"},
		{Name: "B", Skills: nil},
		{Name: "C", Skills: []string{"sc"}, Experience: "senior"},
	}
	descriptions := []string{"desc-A", "desc-B-skipped", "desc-C"}
	res := AssignLevels(vacs, descriptions)
	if len(res.Levels) != 2 {
		t.Fatalf("ожидали 2 уровня, получили %d", len(res.Levels))
	}
	if res.Levels[0].Description != "desc-A" || res.Levels[1].Description != "desc-C" {
		t.Fatalf("descriptions выровнены неверно: [%q, %q]", res.Levels[0].Description, res.Levels[1].Description)
	}
}
