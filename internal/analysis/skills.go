package analysis

import (
	"strings"
	"sync"
)

type Catalog struct {
	mu     sync.RWMutex
	skills []string
}

func NewCatalog(skills []string) *Catalog {
	lower := make([]string, len(skills))
	for i, s := range skills {
		lower[i] = strings.ToLower(strings.TrimSpace(s))
	}
	return &Catalog{skills: lower}
}

func (c *Catalog) Skills() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, len(c.skills))
	copy(out, c.skills)
	return out
}

type ResultTriple struct {
	Freq       map[string]int
	WeightsRaw map[string]float64
	PerVacancy [][]string
}

func ExtractSkills(texts []string, catalog *Catalog) ResultTriple {
	freq := map[string]int{}
	perVacancy := make([][]string, len(texts))
	totalTokens := 0

	catalog.mu.RLock()
	skills := catalog.skills
	catalog.mu.RUnlock()

	for idx, text := range texts {
		tokens := tokenize(text)
		totalTokens += len(tokens)

		tokenSet := make(map[string]struct{}, len(tokens))
		for _, t := range tokens {
			tokenSet[t] = struct{}{}
		}

		foundInThis := []string{}
		for _, skill := range skills {
			if skill == "" {
				continue
			}
			if containsSkill(skill, tokenSet, text) {
				freq[skill]++
				foundInThis = append(foundInThis, skill)
			}
		}
		perVacancy[idx] = foundInThis
	}

	weightsRaw := make(map[string]float64, len(freq))
	if totalTokens > 0 {
		for sk, f := range freq {
			weightsRaw[sk] = float64(f) / float64(totalTokens)
		}
	}

	return ResultTriple{Freq: freq, WeightsRaw: weightsRaw, PerVacancy: perVacancy}
}

func containsSkill(skill string, tokenSet map[string]struct{}, rawText string) bool {
	if strings.Contains(skill, " ") {
		return strings.Contains(strings.ToLower(rawText), skill)
	}
	_, ok := tokenSet[skill]
	return ok
}

func tokenize(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		switch {
		case r >= 'a' && r <= 'z':
			return false
		case r >= '0' && r <= '9':
			return false
		case r == '_':
			return false
		default:
			return true
		}
	})
	return fields
}
