package analysis

import (
	"strings"
	"sync"
)

type Catalog struct {
	mu       sync.RWMutex
	skills   []string
	taxonomy *Taxonomy
}

func NewCatalog(skills []string) *Catalog {
	return newCatalog(skills, nil)
}

func NewTaxonomyCatalog(skills []string, taxonomy *Taxonomy) *Catalog {
	return newCatalog(skills, taxonomy)
}

func newCatalog(skills []string, taxonomy *Taxonomy) *Catalog {
	normalized := make([]string, 0, len(skills))
	seen := make(map[string]struct{}, len(skills))
	for _, s := range skills {
		s = normalizeSkill(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		normalized = append(normalized, s)
	}
	return &Catalog{skills: normalized, taxonomy: taxonomy}
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
	if catalog == nil {
		return ResultTriple{Freq: freq, WeightsRaw: map[string]float64{}, PerVacancy: perVacancy}
	}

	catalog.mu.RLock()
	skills := append([]string(nil), catalog.skills...)
	taxonomy := catalog.taxonomy
	catalog.mu.RUnlock()

	for idx, text := range texts {
		tokens := tokenize(text)
		totalTokens += len(tokens)
		tokenSet := make(map[string]struct{}, len(tokens))
		for _, token := range tokens {
			tokenSet[token] = struct{}{}
		}

		found := make(map[string]struct{})
		for _, term := range skills {
			if !containsSkill(term, tokenSet, text) {
				continue
			}
			canonical := term
			if taxonomy != nil {
				canonical = taxonomy.Canonical(term)
			}
			found[canonical] = struct{}{}
		}
		for skill := range found {
			freq[skill]++
			perVacancy[idx] = append(perVacancy[idx], skill)
		}
	}

	weights := make(map[string]float64, len(freq))
	if totalTokens > 0 {
		for skill, count := range freq {
			weights[skill] = float64(count) / float64(totalTokens)
		}
	}
	return ResultTriple{Freq: freq, WeightsRaw: weights, PerVacancy: perVacancy}
}

func containsSkill(skill string, tokenSet map[string]struct{}, rawText string) bool {
	if strings.Contains(skill, " ") {
		return strings.Contains(strings.ToLower(rawText), skill)
	}
	_, ok := tokenSet[skill]
	return ok
}

func tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		switch {
		case r >= 'a' && r <= 'z':
			return false
		case r >= '0' && r <= '9':
			return false
		case r >= 'а' && r <= 'я':
			return false
		case r == 'ё', r == '_', r == '+', r == '#':
			return false
		default:
			return true
		}
	})
}
