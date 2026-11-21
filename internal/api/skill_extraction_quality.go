package api

import (
	"activity-descriptor/internal/domain"
	"strings"
	"unicode"
)

// Model output may refer to a dictionary tool by its displayed name or alias,
// or repeat the same tool with separate usages. Canonicalize only unambiguous
// dictionary matches and merge compatible repetitions; unknown references and
// conflicting versions are left for strict validation to reject.
func normalizeSkillExtractionToolReferences(candidate *domain.SkillModel, previous domain.SkillModel) {
	saved := make(map[string]struct{}, len(previous.Definitions))
	for _, definition := range previous.Definitions {
		saved[domain.SkillKey(definition.ID, definition.Version)] = struct{}{}
	}

	lookup := map[string]string{}
	ambiguous := map[string]bool{}
	for _, tool := range candidate.Tools {
		names := append([]string{tool.ID, tool.Name}, tool.Aliases...)
		for _, name := range names {
			key := skillToolReferenceKey(name)
			if key == "" || ambiguous[key] {
				continue
			}
			if existing, ok := lookup[key]; ok && existing != tool.ID {
				delete(lookup, key)
				ambiguous[key] = true
				continue
			}
			lookup[key] = tool.ID
		}
	}

	for i := range candidate.Definitions {
		definition := &candidate.Definitions[i]
		if _, exists := saved[domain.SkillKey(definition.ID, definition.Version)]; exists {
			continue
		}
		uses := make([]domain.SkillToolUse, 0, len(definition.Tools))
		indexByID := map[string]int{}
		for _, use := range definition.Tools {
			if canonical, ok := lookup[skillToolReferenceKey(use.ToolID)]; ok {
				use.ToolID = canonical
			}
			index, duplicate := indexByID[use.ToolID]
			if !duplicate || uses[index].Version != use.Version {
				indexByID[use.ToolID] = len(uses)
				uses = append(uses, use)
				continue
			}
			current := &uses[index]
			if current.Usage == "" {
				current.Usage = use.Usage
			} else if use.Usage != "" && domain.SkillText(current.Usage) != domain.SkillText(use.Usage) {
				current.Usage += "; " + use.Usage
			}
			current.Required = current.Required || use.Required
		}
		definition.Tools = uses
	}
}

func skillToolReferenceKey(value string) string {
	var key strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			key.WriteRune(r)
		}
	}
	return key.String()
}

// Evidence is authoritative for node ownership. If the model associates a
// valid quote with the wrong node, move the proposal to the quote's actual
// node; when a link cites multiple nodes, split it into one grounded link per
// node instead of fabricating an evidence relationship.
func normalizeSkillExtractionEvidenceOwners(links []domain.SkillNodeLink, nodes []domain.Node) []domain.SkillNodeLink {
	evidenceOwners := map[string]string{}
	for _, node := range nodes {
		for _, evidence := range node.Evidence {
			evidenceOwners[evidence.ID] = node.ID
		}
	}
	out := make([]domain.SkillNodeLink, 0, len(links))
	for _, link := range links {
		ownerOrder := []string{}
		groups := map[string][]string{}
		seen := map[string]bool{}
		for _, evidenceID := range link.EvidenceIDs {
			if seen[evidenceID] {
				continue
			}
			seen[evidenceID] = true
			owner, ok := evidenceOwners[evidenceID]
			if !ok {
				owner = link.NodeID // Preserve unknown IDs so strict validation rejects them.
			}
			if _, exists := groups[owner]; !exists {
				ownerOrder = append(ownerOrder, owner)
			}
			groups[owner] = append(groups[owner], evidenceID)
		}
		if len(ownerOrder) == 0 {
			out = append(out, link)
			continue
		}
		for _, owner := range ownerOrder {
			grounded := link
			grounded.ID = ""
			grounded.NodeID = owner
			grounded.EvidenceIDs = groups[owner]
			out = append(out, grounded)
		}
	}
	return out
}

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
