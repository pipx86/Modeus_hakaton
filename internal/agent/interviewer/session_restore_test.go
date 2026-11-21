package interviewer

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

// errorClient always fails, so the session degrades to fallback questions.
type errorClient struct{}

func (errorClient) Chat(context.Context, []llm.Message) (string, error) {
	return "", errors.New("provider is down")
}

func newRestoreTestSession(t *testing.T, role string, levels []domain.QuestionLevel) (*Session, *roleAwareClient) {
	t.Helper()
	client := &roleAwareClient{t: t, role: role, levels: levels}
	agentInstance, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	session, err := NewSession(agentInstance, "sess-restore", domain.Respondent{
		ID:         "resp-restore",
		Role:       role,
		Profession: role,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session, client
}

func TestSessionSnapshotRoundTripKeepsCursor(t *testing.T) {
	levels := []domain.QuestionLevel{
		domain.QuestionRole,
		domain.QuestionFunction,
		domain.QuestionTask,
		domain.QuestionAction,
	}
	session, _ := newRestoreTestSession(t, "Аналитик данных", levels)

	first, err := session.Next(context.Background())
	if err != nil {
		t.Fatalf("first Next: %v", err)
	}
	if err := session.Answer("Я готовлю управленческую отчётность и проверяю качество данных."); err != nil {
		t.Fatalf("first Answer: %v", err)
	}
	second, err := session.Next(context.Background())
	if err != nil {
		t.Fatalf("second Next: %v", err)
	}
	snapshot := session.Snapshot()
	if snapshot.State.Status != domain.SessionInProgress {
		t.Fatalf("snapshot status = %q, want in_progress", snapshot.State.Status)
	}
	if got, want := len(snapshot.Plan), len(levels); got != want {
		t.Fatalf("snapshot plan length = %d, want %d", got, want)
	}
	if got, want := len(snapshot.State.Questions), 2; got != want {
		t.Fatalf("snapshot questions = %d, want %d", got, want)
	}
	if got, want := len(snapshot.State.Answers), 1; got != want {
		t.Fatalf("snapshot answers = %d, want %d", got, want)
	}

	restored, err := RestoreSession(session.agent, snapshot)
	if err != nil {
		t.Fatalf("RestoreSession: %v", err)
	}
	if !reflect.DeepEqual(restored.State(), session.State()) {
		t.Fatalf("restored state differs:\n got %+v\nwant %+v", restored.State(), session.State())
	}
	if !reflect.DeepEqual(restored.Last(), session.Last()) {
		t.Fatalf("restored last output = %+v, want %+v", restored.Last(), session.Last())
	}
	if second.ID != "q-2" {
		t.Fatalf("second question id = %q, want q-2", second.ID)
	}
	if !restored.PendingAnswer() {
		t.Fatal("restored session lost the open question")
	}

	third, err := restored.Next(context.Background())
	if err != nil {
		t.Fatalf("next after restore: %v", err)
	}
	if third.ID != "q-3" {
		t.Fatalf("question id after restore = %q, want q-3", third.ID)
	}
	if third.Level != domain.QuestionTask {
		t.Fatalf("question level after restore = %q, want task", third.Level)
	}
	if other := restored.Last().Question; other.ID != third.ID {
		t.Fatalf("last output after restore = %q, want %q", other.ID, third.ID)
	}
	_ = first
}

func TestSessionSnapshotKeepsFallbacksAndError(t *testing.T) {
	// A session whose model always fails degrades to fallback questions.
	agentInstance, err := New(errorClient{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	session, err := NewSession(agentInstance, "sess-degraded", domain.Respondent{
		ID:         "resp-degraded",
		Role:       "Аналитик",
		Profession: "Аналитик",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := session.Next(context.Background()); err != nil {
			t.Fatalf("Next %d: %v", i, err)
		}
		if err := session.Answer("Повторяющийся конкретный ответ для проверки деградации."); err != nil {
			t.Fatalf("Answer %d: %v", i, err)
		}
	}
	snapshot := session.Snapshot()
	if snapshot.Fallbacks != 2 {
		t.Fatalf("snapshot fallbacks = %d, want 2", snapshot.Fallbacks)
	}
	if snapshot.LastError == "" {
		t.Fatal("snapshot lost the model error")
	}
	if snapshot.State.Status != domain.SessionDegraded {
		t.Fatalf("snapshot status = %q, want degraded", snapshot.State.Status)
	}

	restored, err := RestoreSession(agentInstance, snapshot)
	if err != nil {
		t.Fatalf("RestoreSession: %v", err)
	}
	if restored.LastError() == nil || !strings.Contains(restored.LastError().Error(), "provider is down") {
		t.Fatalf("restored last error = %v", restored.LastError())
	}
	if restored.Done() {
		t.Fatal("session with two fallbacks must not be done yet")
	}
	if _, err := restored.Next(context.Background()); err != nil {
		t.Fatalf("Next after restore: %v", err)
	}
	if restored.Done() {
		t.Fatal("session must still ask the fourth question")
	}
	if _, err := restored.Next(context.Background()); err != nil {
		t.Fatalf("fourth Next after restore: %v", err)
	}
	if !restored.Done() {
		t.Fatal("session must stop after the fourth fallback question")
	}
}

func TestRestoreSessionShortensLegacySixQuestionPlan(t *testing.T) {
	levels := []domain.QuestionLevel{
		domain.QuestionRole, domain.QuestionFunction, domain.QuestionTask,
		domain.QuestionAction, domain.QuestionTask, domain.QuestionAction,
	}
	session, _ := newRestoreTestSession(t, "Аналитик данных", levels)
	snapshot := session.Snapshot()
	snapshot.Plan = levels
	restored, err := RestoreSession(session.agent, snapshot)
	if err != nil {
		t.Fatalf("RestoreSession: %v", err)
	}
	for range coreQuestionCount {
		if _, err := restored.Next(context.Background()); err != nil {
			t.Fatalf("Next: %v", err)
		}
		if err := restored.Answer("Готовлю отчёты и проверяю данные каждый день."); err != nil {
			t.Fatalf("Answer: %v", err)
		}
	}
	if !restored.Done() || len(restored.State().Questions) != coreQuestionCount {
		t.Fatalf("legacy plan finished with %d questions", len(restored.State().Questions))
	}

	snapshot = restored.Snapshot()
	snapshot.Plan = levels
	snapshot.State.Questions = append(snapshot.State.Questions, domain.Question{
		ID: "q-5", Level: domain.QuestionTask, Text: "Какую ещё задачу вы выполняете?",
	})
	restored, err = RestoreSession(session.agent, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Done() || restored.PendingAnswer() || len(restored.State().Answers) != coreQuestionCount {
		t.Fatal("a pending fifth question must not block analysis of the four saved answers")
	}
}

func TestRestoreSessionRejectsCorruptSnapshots(t *testing.T) {
	levels := []domain.QuestionLevel{domain.QuestionRole, domain.QuestionFunction}
	session, client := newRestoreTestSession(t, "Аналитик", levels)
	if _, err := session.Next(context.Background()); err != nil {
		t.Fatalf("Next: %v", err)
	}
	valid := session.Snapshot()
	agentInstance, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cases := map[string]func(*Snapshot){
		"empty id":         func(s *Snapshot) { s.State.ID = "" },
		"empty profession": func(s *Snapshot) { s.State.Profession = "" },
		"empty plan":       func(s *Snapshot) { s.Plan = nil },
		"invalid level":    func(s *Snapshot) { s.Plan[0] = "unknown" },
		"too many answers": func(s *Snapshot) { s.State.Answers = append(s.State.Answers, domain.Answer{}, domain.Answer{}) },
		"invalid fallback": func(s *Snapshot) { s.Fallbacks = -1 },
		"invalid status":   func(s *Snapshot) { s.State.Status = "broken" },
		"bad cursor":       func(s *Snapshot) { s.ClarifiedFor = 3 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			corrupt := valid
			mutate(&corrupt)
			if _, err := RestoreSession(agentInstance, corrupt); err == nil {
				t.Fatalf("RestoreSession accepted corrupt snapshot: %+v", corrupt)
			}
		})
	}
}
