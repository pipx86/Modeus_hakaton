package api

import (
	"activity-descriptor/internal/domain"
	"strings"
)

// Preserve the citation, but do not count "not discussed" as a known condition.
func normalizeSkillUnknowns(candidate *domain.SkillModel, old domain.SkillModel) {
	known := map[string]bool{}
	for _, d := range old.Definitions {
		known[domain.SkillKey(d.ID, d.Version)] = true
	}
	for i := range candidate.Definitions {
		d := &candidate.Definitions[i]
		if known[domain.SkillKey(d.ID, d.Version)] {
			continue
		}
		gaps := []string{}
		for _, field := range []struct {
			value *string
			label string
		}{{&d.Conditions, "условия"}, {&d.Result, "результат"}, {&d.Criterion, "критерий"}, {&d.Autonomy, "самостоятельность"}, {&d.Scope, "диапазон ситуаций"}} {
			if strings.TrimSpace(*field.value) != "" && domain.SkillFieldMissing(*field.value) {
				*field.value = ""
				gaps = append(gaps, field.label)
			}
		}
		if len(gaps) > 0 {
			for j := range candidate.Links {
				link := &candidate.Links[j]
				if link.SkillRef.ID == d.ID && link.Version == d.Version && strings.TrimSpace(link.Reason) != "" {
					link.Reason += "; требуют уточнения: " + strings.Join(gaps, ", ")
				}
			}
		}
	}
}
