package analyst

import (
	"regexp"
	"strings"
	"unicode"

	"activity-descriptor/internal/agent"

	"github.com/kljensen/snowball/russian"
)

var titleTokenPattern = regexp.MustCompile(`[\p{L}\p{N}][\p{L}\p{N}+#./]*`)

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
	return string(entity.Type) + "|" + parent + "|" + morphologyTitleKey(entity.Title)
}

// normalizeTitle lowercases, trims punctuation and collapses whitespace so
// "  Писать  SQL-запросы. " and "писать sql-запросы" compare equal.
func normalizeTitle(title string) string {
	normalized := strings.ToLower(strings.TrimSpace(title))
	normalized = strings.Trim(normalized, " .,:;!?…«»\"'()-")
	return strings.Join(strings.Fields(normalized), " ")
}

// morphologyTitleKey is used only to compare node titles. It keeps the title
// itself intact and stems standalone Russian words so common inflections do
// not produce duplicate tasks or actions. Technical tokens such as SQL, C++,
// C#, Go and 1C are intentionally preserved unchanged.
func morphologyTitleKey(title string) string {
	tokens := titleTokenPattern.FindAllString(normalizeTitle(title), -1)
	for i, token := range tokens {
		if isRussianWord(token) {
			tokens[i] = russian.Stem(token, false)
		}
	}
	return strings.Join(tokens, " ")
}

func isRussianWord(value string) bool {
	hasLetter := false
	for _, r := range value {
		if !unicode.IsLetter(r) {
			return false
		}
		hasLetter = true
		if !(r >= 'а' && r <= 'я' || r >= 'А' && r <= 'Я' || r == 'ё' || r == 'Ё') {
			return false
		}
	}
	return hasLetter
}
