package interviewer

import (
	"testing"

	"activity-descriptor/internal/domain"
)

func TestScoreAnswerRewardsConcreteResult(t *testing.T) {
	strong, _ := ScoreAnswer("Я настроил CI/CD в GitLab: сократил время сборки на 40% и уменьшил число инцидентов на 3 в месяц.")
	weak, _ := ScoreAnswer("Ну, что-то делал по-разному.")
	if strong.Total <= weak.Total || strong.Total < 50 {
		t.Fatalf("strong=%d weak=%d", strong.Total, weak.Total)
	}
}

func TestContradictionsFindsDeniedGitHubSkill(t *testing.T) {
	got := Contradictions([]string{"Docker", "Kubernetes"}, "С Docker не работал, Kubernetes не использовал.")
	if len(got) != 2 {
		t.Fatalf("contradictions = %#v", got)
	}
}

func TestProgressCountsTopicsAndRemaining(t *testing.T) {
	agentInstance, err := New(&scriptedClient{t: t})
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(agentInstance, "coverage-1", domain.Respondent{Profession: "Backend-разработчик"})
	if err != nil {
		t.Fatal(err)
	}
	session.state.Questions = []domain.Question{{ID: "q-1", Text: "Как вы тестируете сервисы?"}}
	session.state.Answers = []domain.Answer{{QuestionID: "q-1", Text: "Пишу Go-тесты и проверяю базы данных.", Quality: 60}}
	progress := session.Progress()
	if progress.Asked != 1 || progress.Remaining != len(session.plan)-1 {
		t.Fatalf("progress = %+v", progress)
	}
	if len(progress.CoveredTopics) == 0 || len(progress.UncoveredTopics) == 0 {
		t.Fatalf("coverage = %+v", progress)
	}
	if progress.AverageQuality != 60 || progress.LastQuality != 60 {
		t.Fatalf("quality = %+v", progress)
	}
}
