package interviewer

import (
	"strings"
	"testing"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/prompts"
)

func TestBuildMessagesAddsSourceContextInstruction(t *testing.T) {
	messages, err := buildMessages(prompts.Document{System: "system", Examples: "examples"}, agent.InterviewerInput{
		SourceContext: "Вакансии и материалы по профессии: Go, SQL",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 4 {
		t.Fatalf("messages = %d, want 4", len(messages))
	}
	if !strings.Contains(messages[3].Content, "справочные данные") {
		t.Fatalf("source context instruction missing: %q", messages[3].Content)
	}
}

func TestBuildMessagesWithoutSourceContextKeepsThreeMessages(t *testing.T) {
	messages, err := buildMessages(prompts.Document{System: "system", Examples: "examples"}, agent.InterviewerInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(messages))
	}
}
