package analyst

import (
	"strings"
	"testing"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
)

func TestFitAnalystInputCapsContext(t *testing.T) {
	in := agent.AnalystInput{
		SessionID:  "s1",
		Profession: "Data Analyst",
		Transcript: strings.Repeat("x", maxAnalystTranscriptRunes+500),
		Sources: func() []domain.Source {
			sources := make([]domain.Source, maxAnalystSources+3)
			for i := range sources {
				sources[i] = domain.Source{
					ID:      "src",
					Title:   "Source",
					Content: strings.Repeat("y", maxAnalystSourceRunes+500),
				}
			}
			return sources
		}(),
	}
	fitted := fitAnalystInput(in)
	if len([]rune(fitted.Transcript)) > maxAnalystTranscriptRunes+len([]rune("\n...[truncated]")) {
		t.Fatalf("transcript not capped: %d", len([]rune(fitted.Transcript)))
	}
	if !strings.HasSuffix(fitted.Transcript, "...[truncated]") {
		t.Fatal("transcript truncation marker missing")
	}
	if len(fitted.Sources) != maxAnalystSources {
		t.Fatalf("sources = %d, want %d", len(fitted.Sources), maxAnalystSources)
	}
	for i, source := range fitted.Sources {
		if len([]rune(source.Content)) > maxAnalystSourceRunes+len([]rune("\n...[truncated]")) {
			t.Fatalf("source %d not capped: %d", i, len([]rune(source.Content)))
		}
	}
	if in.Sources[0].Content == fitted.Sources[0].Content {
		t.Fatal("fitAnalystInput mutated the caller's source slice")
	}
}

func TestFitAnalystInputKeepsSmallInput(t *testing.T) {
	in := agent.AnalystInput{Transcript: "short", Sources: []domain.Source{{Content: "small"}}}
	fitted := fitAnalystInput(in)
	if fitted.Transcript != "short" || fitted.Sources[0].Content != "small" {
		t.Fatalf("small input changed: %#v", fitted)
	}
}
