package analysis

import (
	"math"
	"sort"
	"strings"
)

type CandidateProfile struct {
	Name   string
	Skills []string
}

type MarketData struct {
	SkillFreq        map[string]int
	HybridWeights    map[string]float64
	VacanciesByLevel [][]string
	// Affinity — матрица «неразрывности» навыков (из ComputeCoOccurrence).
	// Если задана, приоритет рекомендаций дополнительно учитывает,
	// насколько missing-навык связан с навыками кандидата.
	Affinity map[string]map[string]float64
}

type Recommendation struct {
	Skill    string  `json:"skill"`
	Priority float64 `json:"priority"`
	Reason   string  `json:"reason"`
}

type GapReport struct {
	ProfileName     string           `json:"profile_name"`
	Coverage        float64          `json:"coverage"`
	MissingSkills   []string         `json:"missing_skills"`
	Recommendations []Recommendation `json:"recommendations"`
}

func ComputeGap(profiles []CandidateProfile, market MarketData, topK int) []GapReport {
	return ComputeGapWithTaxonomy(profiles, market, topK, DefaultTaxonomy())
}

func ComputeGapWithTaxonomy(profiles []CandidateProfile, market MarketData, topK int, taxonomy *Taxonomy) []GapReport {
	if topK <= 0 {
		topK = 10
	}
	canonicalFreq := make(map[string]int, len(market.SkillFreq))
	canonicalWeight := make(map[string]float64, len(market.HybridWeights))
	for skill, freq := range market.SkillFreq {
		key := taxonomy.Canonical(skill)
		canonicalFreq[key] += freq
		canonicalWeight[key] += market.HybridWeights[skill]
	}
	reports := make([]GapReport, 0, len(profiles))
	for _, profile := range profiles {
		candidateSet := make(map[string]struct{}, len(profile.Skills))
		for _, skill := range profile.Skills {
			candidateSet[taxonomy.Canonical(skill)] = struct{}{}
		}
		matched := 0
		missing := make([]string, 0)
		for skill := range canonicalFreq {
			if _, ok := candidateSet[skill]; ok {
				matched++
			} else {
				missing = append(missing, skill)
			}
		}
		coverage := 0.0
		if len(canonicalFreq) > 0 {
			coverage = float64(matched) / float64(len(canonicalFreq))
		}
		recs := make([]Recommendation, 0, len(missing))
		for _, skill := range missing {
			freq := canonicalFreq[skill]
			weight := canonicalWeight[skill]
			priority := weight * math.Log(float64(freq)+1)
			priority = BoostPriorityByAffinity(priority, skill, profile.Skills, market.Affinity, 0.35)
			recs = append(recs, Recommendation{
				Skill:    skill,
				Priority: priority,
				Reason:   "missing in profile, high demand in market",
			})
		}
		sort.Slice(recs, func(i, j int) bool {
			if recs[i].Priority == recs[j].Priority {
				return strings.Compare(recs[i].Skill, recs[j].Skill) < 0
			}
			return recs[i].Priority > recs[j].Priority
		})
		if len(recs) > topK {
			recs = recs[:topK]
		}
		sort.Strings(missing)
		reports = append(reports, GapReport{
			ProfileName: profile.Name, Coverage: coverage,
			MissingSkills: missing, Recommendations: recs,
		})
	}
	return reports
}
