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
	if topK <= 0 {
		topK = 10
	}

	reports := make([]GapReport, 0, len(profiles))

	for _, prof := range profiles {
		candidateSet := make(map[string]struct{}, len(prof.Skills))
		for _, s := range prof.Skills {
			candidateSet[strings.ToLower(strings.TrimSpace(s))] = struct{}{}
		}

		var matched, missing []string
		for skill := range market.SkillFreq {
			if _, ok := candidateSet[skill]; ok {
				matched = append(matched, skill)
			} else {
				missing = append(missing, skill)
			}
		}

		totalMarket := len(market.SkillFreq)
		coverage := 0.0
		if totalMarket > 0 {
			coverage = float64(len(matched)) / float64(totalMarket)
		}

		recs := make([]Recommendation, 0, len(missing))
		for _, sk := range missing {
			freq := market.SkillFreq[sk]
			weight := market.HybridWeights[sk]
			priority := weight * math.Log(float64(freq)+1.0)
			recs = append(recs, Recommendation{
				Skill:    sk,
				Priority: priority,
				Reason:   "missing in profile, high demand in market",
			})
		}

		sort.Slice(recs, func(i, j int) bool {
			return recs[i].Priority > recs[j].Priority
		})

		if len(recs) > topK {
			recs = recs[:topK]
		}

		reports = append(reports, GapReport{
			ProfileName:     prof.Name,
			Coverage:        coverage,
			MissingSkills:   missing,
			Recommendations: recs,
		})
	}

	return reports
}
