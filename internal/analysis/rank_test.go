package analysis

import (
	"math"
	"testing"
)

func TestRankSkillsOrderByScore(t *testing.T) {
	freq := map[string]int{"a": 10, "b": 100, "c": 1}
	weights := map[string]float64{"a": 0.1, "b": 0.8, "c": 0.5}

	ranked := RankSkills(freq, weights)
	if len(ranked) != 3 {
		t.Fatalf("ожидали 3, получили %d", len(ranked))
	}
	if ranked[0].Skill != "b" || ranked[1].Skill != "c" || ranked[2].Skill != "a" {
		t.Fatalf("неверный порядок: %+v", ranked)
	}
	if ranked[0].Rank != 1 || ranked[2].Rank != 3 {
		t.Fatalf("ранги сбиты: %+v", ranked)
	}
}

func TestRankSkillsEmpty(t *testing.T) {
	if RankSkills(nil, nil) != nil {
		t.Fatal("пустой ввод должен давать nil")
	}
}

func TestRankSkillsScoreFormula(t *testing.T) {
	freq := map[string]int{"python": 50}
	weights := map[string]float64{"python": 0.4}
	ranked := RankSkills(freq, weights)
	want := 0.4 * math.Log(51)
	if absDiff(ranked[0].Score, want) > 1e-9 {
		t.Fatalf("score: got %.6f, want %.6f", ranked[0].Score, want)
	}
}

func TestComputeCoOccurrenceBasic(t *testing.T) {
	perVacancy := [][]string{
		{"python", "sql", "docker"},
		{"python", "sql"},
		{"python", "go"},
		{"sql", "docker"},
		{"python", "sql", "docker"},
	}

	res := ComputeCoOccurrence(perVacancy, 2)
	if len(res.Pairs) == 0 {
		t.Fatal("ожидали хотя бы одну пару")
	}

	found := false
	for _, p := range res.Pairs {
		if (p.SkillA == "python" && p.SkillB == "sql") || (p.SkillA == "sql" && p.SkillB == "python") {
			found = true
			if p.Count != 3 {
				t.Fatalf("python-sql count: got %d, want 3", p.Count)
			}
			if absDiff(p.Score, 0.6) > 1e-9 {
				t.Fatalf("python-sql jaccard: got %.4f, want 0.6", p.Score)
			}
			break
		}
	}
	if !found {
		t.Fatal("пара python-sql не найдена")
	}
	if res.Affinity["python"]["sql"] <= 0 {
		t.Fatal("affinity matrix должна быть заполнена")
	}
}

func TestComputeCoOccurrenceMinCount(t *testing.T) {
	perVacancy := [][]string{
		{"a", "b"},
		{"a", "c"},
	}
	res := ComputeCoOccurrence(perVacancy, 2)
	if len(res.Pairs) != 0 {
		t.Fatalf("при minCount=2 пар быть не должно, получили %+v", res.Pairs)
	}
}

func TestBoostPriorityByAffinity(t *testing.T) {
	affinity := map[string]map[string]float64{
		"docker": {"python": 0.7, "kubernetes": 0.9},
	}
	base := 1.0
	boosted := BoostPriorityByAffinity(base, "docker", []string{"python"}, affinity, 0.5)
	if absDiff(boosted, 1.35) > 1e-9 {
		t.Fatalf("boosted: got %.4f, want 1.35", boosted)
	}
	noBoost := BoostPriorityByAffinity(base, "docker", []string{"java"}, affinity, 0.5)
	if absDiff(noBoost, 1.0) > 1e-9 {
		t.Fatalf("без связи boost не должен менять: %.4f", noBoost)
	}
}

func TestComputeGapUsesAffinity(t *testing.T) {
	profiles := []CandidateProfile{
		{Name: "Dev", Skills: []string{"python"}},
	}
	market := MarketData{
		SkillFreq:     map[string]int{"docker": 20, "java": 20},
		HybridWeights: map[string]float64{"docker": 0.3, "java": 0.3},
		Affinity: map[string]map[string]float64{
			"docker": {"python": 0.8},
		},
	}
	reports := ComputeGap(profiles, market, 10)
	if len(reports) != 1 || len(reports[0].Recommendations) < 2 {
		t.Fatalf("неожиданный отчёт: %+v", reports)
	}
	if reports[0].Recommendations[0].Skill != "docker" {
		t.Fatalf("ожидали docker первым из-за affinity, получили %+v", reports[0].Recommendations)
	}
}

func TestClusterRelatedSkillsFromAffinity(t *testing.T) {
	affinity := map[string]map[string]float64{
		"python": {"sql": 0.7, "docker": 0.5},
		"sql":    {"python": 0.7, "docker": 0.4},
		"docker": {"python": 0.5, "sql": 0.4},
		"java":   {"spring": 0.8},
		"spring": {"java": 0.8},
	}

	clusters := ClusterRelatedSkills(affinity, nil, 0.35)
	if len(clusters) < 2 {
		t.Fatalf("ожидали ≥2 кластера, получили %d: %+v", len(clusters), clusters)
	}

	foundPythonCluster := false
	for _, c := range clusters {
		hasP, hasS, hasD := false, false, false
		for _, s := range c.Skills {
			switch s {
			case "python":
				hasP = true
			case "sql":
				hasS = true
			case "docker":
				hasD = true
			}
		}
		if hasP && hasS && hasD {
			foundPythonCluster = true
			break
		}
	}
	if !foundPythonCluster {
		t.Fatalf("python+sql+docker должны быть в одном кластере: %+v", clusters)
	}
}

func TestClusterRelatedSkillsWithTaxonomy(t *testing.T) {
	clusters := ClusterRelatedSkills(nil, DefaultTaxonomy(), 0.3)
	if len(clusters) == 0 {
		t.Fatal("taxonomy.Related должна давать хотя бы один кластер")
	}

	found := false
	for _, c := range clusters {
		hasGo, hasPg := false, false
		for _, s := range c.Skills {
			if s == "go" {
				hasGo = true
			}
			if s == "postgresql" {
				hasPg = true
			}
		}
		if hasGo && hasPg {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("go и postgresql должны оказаться в одном кластере через Related: %+v", clusters)
	}
}

func TestClusterRelatedSkillsIgnoresSingletons(t *testing.T) {
	affinity := map[string]map[string]float64{
		"lonely": {},
	}
	clusters := ClusterRelatedSkills(affinity, nil, 0.3)
	if len(clusters) != 0 {
		t.Fatalf("одиночные навыки не должны становиться кластерами: %+v", clusters)
	}
}
