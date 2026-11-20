// Package sourcecluster groups sources by profession before they reach an
// agent. It is deliberately deterministic: the same profession and source set
// always produce the same selected cluster.
package sourcecluster

import (
	"sort"
	"strings"
	"unicode"

	"activity-descriptor/internal/domain"
)

// Cluster is one profession group prepared for an agent.
type Cluster struct {
	Profession string
	SourceIDs  []string
}

// Result is the selected source set plus the grouping metadata.
type Result struct {
	Profession string
	Sources    []domain.Source
	Clusters   []Cluster
	Excluded   int
}

// Select keeps sources relevant to profession and explicit run sources. Explicit
// sources are sources the researcher attached to the run on purpose; they are
// trusted even when their text does not repeat the profession name.
func Select(profession string, sources []domain.Source, explicitIDs map[string]bool) Result {
	targetTokens := tokens(profession)
	result := Result{Profession: strings.TrimSpace(profession)}
	if len(targetTokens) == 0 {
		result.Sources = append([]domain.Source(nil), sources...)
		return result
	}

	selected := make([]domain.Source, 0, len(sources))
	seen := make(map[string]bool, len(sources))
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		key := source.ID
		if key == "" {
			key = source.Title + "\x00" + source.Content
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		if source.Type == domain.SourceInterview {
			continue
		}
		if explicitIDs[source.ID] {
			selected = append(selected, source)
			if source.ID != "" {
				ids = append(ids, source.ID)
			}
			continue
		}
		if scoreSource(targetTokens, source) == 0 {
			result.Excluded++
			continue
		}
		selected = append(selected, source)
		if source.ID != "" {
			ids = append(ids, source.ID)
		}
	}
	sort.Strings(ids)
	result.Sources = selected
	if len(ids) > 0 {
		result.Clusters = []Cluster{{Profession: strings.TrimSpace(profession), SourceIDs: ids}}
	}
	return result
}

func scoreSource(targetTokens []string, source domain.Source) int {
	titleTokens := tokenSet(source.Title)
	contentTokens := tokenSet(source.Content)
	score := 0
	for _, target := range targetTokens {
		if titleTokens[target] {
			score += 3
			continue
		}
		if contentTokens[target] {
			score++
		}
	}
	return score
}

func tokenSet(value string) map[string]bool {
	set := make(map[string]bool)
	for _, token := range tokens(value) {
		set[token] = true
	}
	return set
}

func tokens(value string) []string {
	fields := strings.FieldsFunc(strings.ToLower(strings.ReplaceAll(value, "ё", "е")), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		token := stem(field)
		if len([]rune(token)) < 3 || seen[token] || stopWord[token] {
			continue
		}
		seen[token] = true
		out = append(out, token)
	}
	return out
}

func stem(value string) string {
	for _, suffix := range []string{"ами", "ями", "ов", "ев", "ий", "ый", "ая", "ое", "ые", "ом", "ем", "ах", "ях", "и", "ы", "а", "я", "у", "ю", "е"} {
		if strings.HasSuffix(value, suffix) && len([]rune(value))-len([]rune(suffix)) >= 3 {
			return strings.TrimSuffix(value, suffix)
		}
	}
	return value
}

var stopWord = map[string]bool{
	"для": true, "или": true, "при": true, "как": true, "что": true,
	"роль": true, "профессия": true, "специалист": true, "опыт": true,
	"работа": true, "требование": true,
}
