package guardrails

import "regexp"

// CategoryJailbreak marks an attempt to remove model safety behaviour.
const CategoryJailbreak = "jailbreak"

var jailbreakRules = []rule{
	{"dan", regexp.MustCompile(`(?i)\bDAN\b`)},
	{"developer_mode", regexp.MustCompile(`(?i)\bdeveloper\s+mode\b`)},
	{"do_anything_now", regexp.MustCompile(`(?i)\bdo\s+anything\s+now\b`)},
	{"jailbreak", regexp.MustCompile(`(?i)\bjailbreak\b`)},
	{"ru_developer_mode", regexp.MustCompile(`(?i)режим\s+разработчика`)},
	{"ru_no_limits", regexp.MustCompile(`(?i)без\s+(каких-либо\s+)?ограничени[йя]`)},
	{"bypass_rules", regexp.MustCompile(`(?i)обой(ди|ти)(те)?\s+(все\s+)?(ограничения|правила)`)},
}

// DetectJailbreak returns jailbreak matches sorted by position.
func DetectJailbreak(text string) []Match {
	return matchRules(jailbreakRules, CategoryJailbreak, text)
}
