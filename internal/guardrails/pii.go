// Package guardrails provides deterministic input/output guards for the agent
// pipeline: PII detection and redaction, prompt-injection detection and
// jailbreak detection. The guards are deliberately rule-based: they are cheap,
// auditable and never call a model.
package guardrails

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
)

// Finding is one detected span of sensitive text. Start and End are byte
// offsets into the scanned string; Hash identifies the match without storing
// the original value.
type Finding struct {
	Type  string
	Start int
	End   int
	Hash  string
}

// PII types returned in Finding.Type.
const (
	PIITypeEmail = "email"
	PIITypePhone = "phone"
	PIITypeCard  = "card"
)

type piiPattern struct {
	kind string
	re   *regexp.Regexp
}

var piiPatterns = []piiPattern{
	{PIITypeEmail, regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)},
	{PIITypeCard, regexp.MustCompile(`\b(?:\d[ -]?){15}\d\b`)},
	{PIITypePhone, regexp.MustCompile(`(?:\+7|8)[\s\-]?\(?\d{3}\)?[\s\-]?\d{3}[\s\-]?\d{2}[\s\-]?\d{2}`)},
}

// DetectPII returns non-overlapping PII findings sorted by position. When spans
// overlap, the earliest and longest span wins.
func DetectPII(text string) []Finding {
	return scanPII(text)
}

// RedactPII replaces every detected PII span with a typed placeholder such as
// [EMAIL] and returns the redacted text together with the findings.
func RedactPII(text string) (string, []Finding) {
	findings := scanPII(text)
	if len(findings) == 0 {
		return text, findings
	}
	var builder strings.Builder
	last := 0
	for _, finding := range findings {
		builder.WriteString(text[last:finding.Start])
		builder.WriteString(placeholder(finding.Type))
		last = finding.End
	}
	builder.WriteString(text[last:])
	return builder.String(), findings
}

// SanitizePII is a convenience wrapper returning only the redacted text.
func SanitizePII(text string) string {
	redacted, _ := RedactPII(text)
	return redacted
}

func scanPII(text string) []Finding {
	candidates := make([]Finding, 0)
	for _, pattern := range piiPatterns {
		for _, location := range pattern.re.FindAllStringIndex(text, -1) {
			if location[0] == location[1] {
				continue
			}
			candidates = append(candidates, Finding{
				Type:  pattern.kind,
				Start: location[0],
				End:   location[1],
				Hash:  hashValue(text[location[0]:location[1]]),
			})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Start != candidates[j].Start {
			return candidates[i].Start < candidates[j].Start
		}
		return candidates[i].End > candidates[j].End
	})
	selected := make([]Finding, 0, len(candidates))
	for _, candidate := range candidates {
		if len(selected) > 0 && candidate.Start < selected[len(selected)-1].End {
			continue
		}
		selected = append(selected, candidate)
	}
	return selected
}

func placeholder(piiType string) string {
	switch piiType {
	case PIITypeEmail:
		return "[EMAIL]"
	case PIITypePhone:
		return "[PHONE]"
	case PIITypeCard:
		return "[CARD]"
	default:
		return "[PII]"
	}
}

func hashValue(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}
