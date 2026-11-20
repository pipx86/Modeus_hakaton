package interviewer

import (
	"regexp"
	"strings"
)

// The patterns below are matched against a normalized question (lowercase,
// single spaces). RE2 \b only understands ASCII word characters, so each phrase
// carries explicit Cyrillic/Latin boundaries; otherwise "вы ведь" would also
// match inside a longer word.
var leadingPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?:^|[^a-zа-яё])вы\s+ведь(?:[^a-zа-яё]|$)`),
	regexp.MustCompile(`(?:^|[^a-zа-яё])ведь\s+вы(?:[^a-zа-яё]|$)`),
	regexp.MustCompile(`(?:^|[^a-zа-яё])правильно\s+ли\s+я\s+понимаю(?:[^a-zа-яё]|$)`),
	regexp.MustCompile(`(?:^|[^a-zа-яё])я\s+правильно\s+понимаю(?:[^a-zа-яё]|$)`),
	regexp.MustCompile(`(?:^|[^a-zа-яё])не\s+так\s+ли(?:[^a-zа-яё]|$)`),
	regexp.MustCompile(`(?:^|[^a-zа-яё])вы\s+же(?:[^a-zа-яё]|$)`),
	regexp.MustCompile(`(?:^|[^a-zа-яё])разве\s+(?:вы\s+)?не(?:[^a-zа-яё]|$)`),
	regexp.MustCompile(`(?:^|[^a-zа-яё])согласны\s+ли\s+вы(?:[^a-zа-яё]|$)`),
	regexp.MustCompile(`(?:^|[^a-zа-яё])подтвердите,?\s+что(?:[^a-zа-яё]|$)`),
	regexp.MustCompile(`(?:^|[^a-zа-яё])наверное,?\s+вы(?:[^a-zа-яё]|$)`),
	regexp.MustCompile(`(?:^|[^a-zа-яё])вы\s+наверняка(?:[^a-zа-яё]|$)`),
	regexp.MustCompile(`(?:^|[^a-zа-яё])как\s+известно(?:[^a-zа-яё]|$)`),
}

// Product-internal terms a respondent cannot be expected to know. "дерево" is
// matched in its singular and plural forms, but "деревянный" is not.
var productJargonPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?:^|[^a-zа-яё])дерев(?:о|а|у|ом|е|ья|ьев|ьям|ьями|ьях)(?:[^a-zа-яё]|$)`),
	regexp.MustCompile(`(?:^|[^a-zа-яё])обосновани(?:е|я|ю|ем|и|й|ям|ями|ях)(?:[^a-zа-яё]|$)`),
	regexp.MustCompile(`(?:^|[^a-zа-яё])обосновать(?:[^a-zа-яё]|$)`),
	regexp.MustCompile(`(?:^|[^a-zа-яё])evidence(?:[^a-zа-яё]|$)`),
	regexp.MustCompile(`(?:^|[^a-zа-яё])kpis?(?:[^a-zа-яё]|$)`),
	regexp.MustCompile(`(?:^|[^a-zа-яё])таксономи[а-яё]*(?:[^a-zа-яё]|$)`),
}

// normalizeQuestion lowercases text and collapses all whitespace so the
// heuristics do not depend on line breaks or repeated spaces.
func normalizeQuestion(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

// LooksLeading reports whether question uses a Russian leading construction such
// as "Вы ведь…", "Вы же…", "Разве не…" or "Правильно ли я понимаю, что…".
//
// The check is a pure, deliberately conservative heuristic: a confirmation
// phrase can be a legitimate clarification, so a true result is a review signal,
// not proof of a bad question. The interviewer prompt forbids leading
// formulations; if the pilot adds deterministic rejection, call LooksLeading
// from Session.normalize after a model response (or from a first-question
// quality gate) before storing the question. It is intentionally not wired into
// the default dialog path yet: replacing a question on a false positive would
// silently change the interview and the fallback counters.
func LooksLeading(question string) bool {
	normalized := normalizeQuestion(question)
	if normalized == "" {
		return false
	}
	for _, pattern := range leadingPatterns {
		if pattern.MatchString(normalized) {
			return true
		}
	}
	return false
}

// HasProductJargon reports whether text contains internal product vocabulary
// that a respondent cannot be expected to know: "дерево" (the activity tree),
// "обоснование", "evidence", "KPI" or "таксономия". The first interview question
// must not contain these terms (MVP card 13).
//
// Like LooksLeading, it is a pure heuristic and currently a review tool rather
// than a dialog branch. Wire it into a first-question quality gate or
// Session.normalize only together with a deliberate fallback policy, because
// rewriting a stored question changes the interview and the fallback metrics.
func HasProductJargon(text string) bool {
	normalized := normalizeQuestion(text)
	if normalized == "" {
		return false
	}
	for _, pattern := range productJargonPatterns {
		if pattern.MatchString(normalized) {
			return true
		}
	}
	return false
}
