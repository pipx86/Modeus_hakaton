package interviewer

import (
	"strings"

	"activity-descriptor/internal/domain"
)

// Progress is the live state of an interview shown to the respondent and used
// by the interviewer to adapt its next question.
type Progress struct {
	Asked           int      `json:"asked"`
	Total           int      `json:"total"`
	Remaining       int      `json:"remaining"`
	AverageQuality  int      `json:"average_quality"`
	LastQuality     int      `json:"last_quality"`
	CoveredTopics   []string `json:"covered_topics"`
	UncoveredTopics []string `json:"uncovered_topics"`
	Contradictions  []string `json:"contradictions,omitempty"`
}

type professionTemplate struct {
	Topics []string
	Levels []string
}

var templates = []struct {
	match string
	value professionTemplate
}{
	{match: "backend", value: professionTemplate{
		Topics: []string{"архитектура", "базы данных", "тестирование", "CI/CD", "наблюдаемость"},
		Levels: []string{"junior", "middle", "senior"},
	}},
	{match: "аналитик", value: professionTemplate{
		Topics: []string{"сбор данных", "SQL", "визуализация", "проверка гипотез", "качество данных"},
		Levels: []string{"junior", "middle", "senior"},
	}},
	{match: "devops", value: professionTemplate{
		Topics: []string{"контейнеры", "CI/CD", "мониторинг", "инциденты", "инфраструктура как код"},
		Levels: []string{"junior", "middle", "senior"},
	}},
	{match: "frontend", value: professionTemplate{
		Topics: []string{"UI", "состояние приложения", "доступность", "производительность", "тестирование"},
		Levels: []string{"junior", "middle", "senior"},
	}},
	{match: "qa", value: professionTemplate{
		Topics: []string{"тест-дизайн", "автотесты", "регрессия", "баг-репорты", "качество релиза"},
		Levels: []string{"junior", "middle", "senior"},
	}},
	{match: "product", value: professionTemplate{
		Topics: []string{"исследование", "гипотезы", "метрики", "приоритизация", "запуск"},
		Levels: []string{"junior", "middle", "senior"},
	}},
	{match: "дизайнер", value: professionTemplate{
		Topics: []string{"исследование", "прототипы", "дизайн-система", "пользовательские сценарии", "тестирование"},
		Levels: []string{"junior", "middle", "senior"},
	}},
}

// TemplateFor returns the competency template for a profession. Unknown
// professions get a generic role/function/task/action template rather than an
// empty plan.
func TemplateFor(profession string) professionTemplate {
	normalized := strings.ToLower(strings.TrimSpace(profession))
	for _, candidate := range templates {
		if strings.Contains(normalized, candidate.match) {
			return candidate.value
		}
	}
	return professionTemplate{
		Topics: []string{"роль", "функции", "задачи", "действия", "инструменты"},
		Levels: []string{"junior", "middle", "senior"},
	}
}

// Progress computes coverage from the questions and answers already stored in
// the session. It never calls the model and is safe to expose through the API.
func (s *Session) Progress() Progress {
	progress := Progress{
		Total:     len(s.plan),
		Asked:     len(s.state.Questions),
		Remaining: len(s.plan) - len(s.state.Questions),
	}
	if progress.Remaining < 0 {
		progress.Remaining = 0
	}
	template := TemplateFor(s.state.Profession)
	transcript := strings.ToLower(transcriptText(s.state))
	covered := make([]string, 0, len(template.Topics))
	uncovered := make([]string, 0, len(template.Topics))
	for _, topic := range template.Topics {
		if strings.Contains(transcript, strings.ToLower(topic)) {
			covered = append(covered, topic)
		} else {
			uncovered = append(uncovered, topic)
		}
	}
	progress.CoveredTopics = covered
	progress.UncoveredTopics = uncovered
	progress.Contradictions = append([]string(nil), s.state.Contradictions...)
	totalQuality := 0
	for _, answer := range s.state.Answers {
		totalQuality += answer.Quality
	}
	if len(s.state.Answers) > 0 {
		progress.AverageQuality = totalQuality / len(s.state.Answers)
		progress.LastQuality = s.state.Answers[len(s.state.Answers)-1].Quality
	}
	return progress
}

func transcriptText(state domain.InterviewSession) string {
	var b strings.Builder
	for _, question := range state.Questions {
		b.WriteString(question.Text)
		b.WriteByte('\n')
	}
	for _, answer := range state.Answers {
		b.WriteString(answer.Text)
		b.WriteByte('\n')
	}
	return b.String()
}
