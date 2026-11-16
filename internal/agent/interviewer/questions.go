// Package interviewer implements the interviewer agent and its session driver.
// The agent asks one question per turn across role, function, task and action
// levels; the session keeps the dialog alive when the model fails.
package interviewer

import (
	"strings"

	"activity-descriptor/internal/domain"
)

// defaultPlan is the level sequence of a standard interview: ground the role,
// then its functions, then tasks and concrete actions. A clarification question
// replaces the next planned level when an answer is too vague.
func defaultPlan() []domain.QuestionLevel {
	return []domain.QuestionLevel{
		domain.QuestionRole,
		domain.QuestionFunction,
		domain.QuestionTask,
		domain.QuestionAction,
		domain.QuestionTask,
		domain.QuestionAction,
	}
}

// vagueMarkers are wording patterns that signal a non-specific answer. They
// complement the length heuristic in IsVague.
var vagueMarkers = []string{
	"всякое", "по-разному", "разное", "как-то", "что-то", "примерно",
	"не знаю", "не помню", "иногда", "бывает", "ну,", "не особо",
}

// minAnswerWords is the minimum number of words needed to ground an entity in
// an answer. Shorter answers are treated as vague and clarified.
const minAnswerWords = 4

// IsVague reports whether an answer lacks the specificity needed to ground an
// activity-tree entity. Empty answers are always vague.
func IsVague(answer string) bool {
	normalized := strings.ToLower(strings.TrimSpace(answer))
	if normalized == "" {
		return true
	}
	if len(strings.Fields(normalized)) < minAnswerWords {
		return true
	}
	for _, marker := range vagueMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

// FallbackQuestion returns a deterministic question for level. It guarantees
// the dialog can continue when the model is unavailable and is always marked
// with Fallback = true by the session.
func FallbackQuestion(level domain.QuestionLevel, profession string) domain.Question {
	target := strings.TrimSpace(profession)
	if target == "" {
		target = "вашей роли"
	}
	text := ""
	switch level {
	case domain.QuestionRole:
		text = "Опишите, пожалуйста, чем вы занимаетесь в роли «" + target +
			"» и за что отвечаете."
	case domain.QuestionFunction:
		text = "Назовите, пожалуйста, крупные направления работы, которые входят в вашу роль."
	case domain.QuestionTask:
		text = "Какие конкретные задачи вы выполняете регулярно в рамках этой работы?"
	case domain.QuestionAction:
		text = "Какие конкретные действия и инструменты вы используете, выполняя эту задачу?"
	default:
		text = "Опишите, пожалуйста, одну конкретную задачу, которую вы выполняли на прошлой неделе."
	}
	return domain.Question{
		Level: level,
		Text:  text,
		Rationale: "Детерминированный вопрос: модель недоступна или ответ " +
			"не прошёл валидацию.",
		Fallback: true,
	}
}

// ClarificationQuestion builds a deterministic clarification question for a
// vague answer. The answer is quoted verbatim (trimmed to a reasonable length)
// so the respondent recognizes it.
func ClarificationQuestion(vagueAnswer string) domain.Question {
	quoted := strings.TrimSpace(vagueAnswer)
	if runes := []rune(quoted); len(runes) > 120 {
		quoted = string(runes[:120]) + "…"
	}
	return domain.Question{
		Level:         domain.QuestionTask,
		Text:          "Уточните, пожалуйста: «" + quoted + "». Приведите конкретный пример задачи, действия или результата.",
		Rationale:     "Ответ слишком общий, чтобы подтвердить его цитатой.",
		Clarification: true,
		Fallback:      true,
	}
}
