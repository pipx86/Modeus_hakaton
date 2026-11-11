package analyst

import (
	"strings"

	"activity-descriptor/internal/agent"
)

// DedupEntities merges entities with the same type, parent and normalized title,
// keeping the first occurrence and unioning their evidence. Confidence and
// description are taken from the strongest duplicate.
func DedupEntities(entities []agent.ExtractedEntity) []agent.ExtractedEntity {
	result := make([]agent.ExtractedEntity, 0, len(entities))
	index := make(map[string]int, len(entities))
	for _, entity := range entities {
		key := dedupKey(entity)
		position, duplicate := index[key]
		if !duplicate {
			index[key] = len(result)
			result = append(result, entity)
			continue
		}
		merged := result[position]
		merged.Evidence = mergeEvidence(merged.Evidence, entity.Evidence)
		if entity.Confidence > merged.Confidence {
			merged.Confidence = entity.Confidence
		}
		if merged.Description == "" {
			merged.Description = entity.Description
		}
		result[position] = merged
	}
	return result
}

func dedupKey(entity agent.ExtractedEntity) string {
	parent := ""
	if entity.ParentID != nil {
		parent = strings.TrimSpace(*entity.ParentID)
	}
	return string(entity.Type) + "|" + parent + "|" + normalizeTitle(entity.Title)
}

// normalizeTitle lowercases, trims punctuation and collapses whitespace so
// "  Писать  SQL-запросы. " and "писать sql-запросы" compare equal.
func normalizeTitle(title string) string {
	normalized := strings.ToLower(strings.TrimSpace(title))
	normalized = strings.Trim(normalized, " .,:;!?…«»\"'()-")
	return strings.Join(strings.Fields(normalized), " ")
}
