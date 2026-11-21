package api

import "activity-descriptor/internal/domain"

// Proficiency is a stronger claim than an ordinary skill proposal. Keep that
// label only when the extraction contains enough explicit fields to review it.
// Previously saved definitions are immutable and never rewritten here.
func demoteIncompleteSkillProficiency(candidate *domain.SkillModel, previous domain.SkillModel) {
	saved := make(map[string]struct{}, len(previous.Definitions))
	for _, definition := range previous.Definitions {
		saved[domain.SkillKey(definition.ID, definition.Version)] = struct{}{}
	}
	for i := range candidate.Definitions {
		definition := &candidate.Definitions[i]
		if definition.Kind != "proficiency" {
			continue
		}
		if _, exists := saved[domain.SkillKey(definition.ID, definition.Version)]; exists {
			continue
		}
		if len(definition.Gaps()) > 0 {
			definition.Kind = "skill"
		}
	}
}
