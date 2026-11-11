package guardrails

import (
	"regexp"
	"sort"
)

// Guard categories returned in Match.Category.
const (
	// CategoryInjection marks an attempt to override agent instructions.
	CategoryInjection = "injection"
)

// Match is one detected hostile phrase.
type Match struct {
	Category string
	Pattern  string
	Start    int
	End      int
}

type rule struct {
	name string
	re   *regexp.Regexp
}

var injectionRules = []rule{
	{"ignore_instructions", regexp.MustCompile(`(?i)\bignore\s+(all\s+|the\s+)?(previous|prior|above)\s+(instructions|rules|prompts?)\b`)},
	{"forget_instructions", regexp.MustCompile(`(?i)\bforget\s+(all\s+)?(previous|prior)\s+instructions\b`)},
	{"system_prompt_exfiltration", regexp.MustCompile(`(?i)\b(show|reveal|print|output|repeat)\s+(me\s+)?(the\s+|your\s+)?system\s+prompt\b`)},
	{"ru_ignore_instructions", regexp.MustCompile(`(?i)забудь(те)?\s+(все\s+)?(предыдущие|прошлые)\s+инструкции`)},
	{"ru_system_prompt", regexp.MustCompile(`(?i)(выведи|покажи|раскрой|повтори)\s+(системный\s+)?промпт`)},
	{"ru_ignore_rules", regexp.MustCompile(`(?i)игнорируй(те)?\s+(все\s+)?(инструкции|правила|указания)`)},
	{"new_role", regexp.MustCompile(`(?i)\b(you are now|act as|from now on you are)\b`)},
	{"ru_new_role", regexp.MustCompile(`(?i)ты\s+теперь`)},
}

// DetectInjection returns prompt-injection matches sorted by position.
func DetectInjection(text string) []Match {
	return matchRules(injectionRules, CategoryInjection, text)
}

// IsSuspicious reports whether text contains a prompt-injection or jailbreak
// pattern.
func IsSuspicious(text string) bool {
	return len(DetectInjection(text)) > 0 || len(DetectJailbreak(text)) > 0
}

// Report aggregates all guardrail findings for one text.
type Report struct {
	Injections []Match
	Jailbreaks []Match
	PII        []Finding
}

// IsSuspicious reports whether the scanned text looks hostile.
func (r Report) IsSuspicious() bool {
	return len(r.Injections) > 0 || len(r.Jailbreaks) > 0
}

// Scan runs every detector against text.
func Scan(text string) Report {
	return Report{
		Injections: DetectInjection(text),
		Jailbreaks: DetectJailbreak(text),
		PII:        DetectPII(text),
	}
}

func matchRules(rules []rule, category, text string) []Match {
	matches := make([]Match, 0)
	for _, rule := range rules {
		for _, location := range rule.re.FindAllStringIndex(text, -1) {
			if location[0] == location[1] {
				continue
			}
			matches = append(matches, Match{
				Category: category,
				Pattern:  rule.name,
				Start:    location[0],
				End:      location[1],
			})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Start != matches[j].Start {
			return matches[i].Start < matches[j].Start
		}
		return matches[i].Pattern < matches[j].Pattern
	})
	return matches
}
