package analysis

import "testing"

func TestAssignLevelsJunior(t *testing.T) {
	vacs := []VacancyInput{
		{
			Name:       "Junior Python Developer",
			Experience: "no experience",
			Skills:     []string{"python", "basic sql"},
		},
	}

	res := AssignLevels(vacs, nil)

	if len(res.Levels) != 1 {
		t.Fatal("expected 1 level")
	}

	if res.Levels[0].Experience != LevelJunior {
		t.Fatalf("expected Junior, got %s", res.Levels[0].Experience)
	}

	if res.Levels[0].Confidence < 0.8 {
		t.Fatalf("low confidence for Junior: %.2f", res.Levels[0].Confidence)
	}

	if res.Levels[0].Category != "BackEnd" {
		t.Fatalf("expected BackEnd, got %s", res.Levels[0].Category)
	}
}

func TestAssignLevelsSenior(t *testing.T) {
	vacs := []VacancyInput{
		{
			Name:       "Senior Backend Engineer",
			Experience: "5+ years",
			Skills:     []string{"kubernetes", "system design", "go"},
		},
	}

	res := AssignLevels(vacs, nil)

	if len(res.Levels) != 1 {
		t.Fatal("expected 1 level")
	}

	if res.Levels[0].Experience != LevelSenior {
		t.Fatalf("expected Senior, got %s", res.Levels[0].Experience)
	}

	if res.Levels[0].Category != "BackEnd" {
		t.Fatalf("expected BackEnd, got %s", res.Levels[0].Category)
	}
}

func TestAssignLevelsDevOps(t *testing.T) {
	vacs := []VacancyInput{
		{
			Name:       "DevOps Engineer",
			Experience: "3 years",
			Skills:     []string{"Docker", "Kubernetes", "Linux"},
		},
	}

	res := AssignLevels(vacs, nil)

	if res.Levels[0].Category != "DevOps" {
		t.Fatalf("expected DevOps, got %s", res.Levels[0].Category)
	}
}

func TestAssignLevelsDataAnalyst(t *testing.T) {
	vacs := []VacancyInput{
		{
			Name:       "Data Analyst",
			Experience: "2 years",
			Skills:     []string{"Python", "Pandas", "SQL", "Tableau"},
		},
	}

	res := AssignLevels(vacs, nil)

	if res.Levels[0].Category != "Data Analyst" {
		t.Fatalf("expected Data Analyst, got %s", res.Levels[0].Category)
	}
}

func TestClusterSkills(t *testing.T) {
	skills := []string{
		"Python",
		"Docker",
		"React",
		"Kubernetes",
		"SQL",
		"Pandas",
	}

	clusters := ClusterSkills(skills)

	if len(clusters["BackEnd"]) < 2 {
		t.Fatal("BackEnd cluster is empty or too small")
	}

	if len(clusters["DevOps"]) < 2 {
		t.Fatal("DevOps cluster is empty or too small")
	}

	if len(clusters["Data Analyst"]) < 1 {
		t.Fatal("Data Analyst cluster is empty")
	}

	if _, exists := clusters["Frontend"]; exists {
		t.Fatal("Frontend category should not exist")
	}
}
