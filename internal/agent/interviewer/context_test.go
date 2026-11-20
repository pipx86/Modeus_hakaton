package interviewer

import (
	"strings"
	"testing"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
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

func TestBuildMessagesAddsProfileContextInstruction(t *testing.T) {
	messages, err := buildMessages(prompts.Document{System: "system", Examples: "examples"}, agent.InterviewerInput{
		ProfileContext: "GitHub @octocat: навыки Go, PostgreSQL.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 4 {
		t.Fatalf("messages = %d, want 4", len(messages))
	}
	if !strings.Contains(messages[3].Content, "profile_context") ||
		!strings.Contains(messages[3].Content, "не ответ интервью") {
		t.Fatalf("profile context instruction missing: %q", messages[3].Content)
	}
}

func TestSessionProfileContextIsPersisted(t *testing.T) {
	agentInstance, err := New(&scriptedClient{t: t})
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(agentInstance, "session-1", domain.Respondent{Profession: "Backend-разработчик"})
	if err != nil {
		t.Fatal(err)
	}
	session.SetProfileContext("GitHub @octocat: Go, PostgreSQL")
	if got := session.State().ProfileContext; got != "GitHub @octocat: Go, PostgreSQL" {
		t.Fatalf("profile context = %q", got)
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
