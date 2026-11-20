package interviewer

import (
	"fmt"
	"strings"

	"activity-descriptor/internal/domain"
)

// FallbackRecommendationsMessage опирается только на профессию и конкретные ответы интервью.
func FallbackRecommendationsMessage(session domain.InterviewSession) string {
	profession := shortRecommendationValue(session.Profession, 80)
	if profession == "" {
		profession = shortRecommendationValue(session.Respondent.Profession, 80)
	}
	recommendations := make([]string, 0, 9)
	if profession != "" {
		recommendations = append(recommendations,
			fmt.Sprintf("Для роли «%s» составьте список основных функций и ожидаемых результатов; согласуйте его с теми, кто оценивает вашу работу.", profession))
	}

	levels := make(map[string]domain.QuestionLevel, len(session.Questions))
	for _, question := range session.Questions {
		levels[question.ID] = question.Level
	}
	seen := make(map[domain.QuestionLevel]bool, 4)
	grounded := make([]string, 0, len(session.Answers))
	for _, answer := range session.Answers {
		if answer.Vague || IsVague(answer.Text) {
			continue
		}
		level := levels[answer.QuestionID]
		value := shortRecommendationValue(answer.Text, 110)
		if value == "" {
			continue
		}
		grounded = append(grounded, strings.ToLower(answer.Text))
		if seen[level] {
			continue
		}
		var suggestion string
		switch level {
		case domain.QuestionRole:
			suggestion = "Вы описали свою роль так: «%s». Уточните границы ответственности и один измеримый результат этой работы."
		case domain.QuestionFunction:
			suggestion = "Для названного направления «%s» зафиксируйте результат, регулярность и критерий качества."
		case domain.QuestionTask:
			suggestion = "Для задачи «%s» запишите входные данные, ожидаемый результат и способ проверки выполнения."
		case domain.QuestionAction:
			suggestion = "Для действия «%s» составьте повторяемый чек-лист и сохраните пример выполненной работы."
		default:
			continue
		}
		recommendations = append(recommendations, fmt.Sprintf(suggestion, value))
		seen[level] = true
	}

	answers := strings.Join(grounded, " ")
	topics := []struct {
		markers []string
		text    string
	}{
		{[]string{"данн", "sql", "отчёт", "отчет", "дашборд"}, "Для упомянутой работы с данными добавьте проверку полноты, точности и происхождения данных перед передачей результата."},
		{[]string{"автомат", "скрипт", "pipeline", "деплой"}, "Для повторяющихся операций опишите ручные шаги, затем автоматизируйте один из них и сравните время выполнения до и после."},
		{[]string{"тест", "проверк", "контрол", "ошиб"}, "Для упомянутых проверок определите типичные ошибки и критерии приёмки; сохраните результаты проверки вместе с задачей."},
		{[]string{"клиент", "заказчик", "команд", "пользоват"}, "Для работы с заинтересованными людьми заранее согласуйте ожидаемый результат и способ получения обратной связи."},
		{[]string{"документ", "регламент", "стандарт", "инструк"}, "Для работы с документами укажите версию и источник требований, затем привяжите к результату конкретный подтверждающий фрагмент."},
		{[]string{"срок", "планир", "приоритет"}, "Для задач со сроками зафиксируйте приоритет, зависимые шаги и момент проверки промежуточного результата."},
	}
	for _, topic := range topics {
		if containsRecommendationMarker(answers, topic.markers) {
			recommendations = append(recommendations, topic.text)
		}
	}
	if len(recommendations) == 0 {
		recommendations = append(recommendations, "Опишите конкретную роль, одну регулярную задачу, свои действия и полученный результат — тогда рекомендации можно будет обосновать вашими ответами.")
	} else if len(grounded) == 0 {
		recommendations = append(recommendations, "Добавьте конкретный пример задачи и результата: текущих ответов недостаточно для более точных рекомендаций.")
	}

	var message strings.Builder
	message.WriteString("Резервные рекомендации по интервью (без ИИ-анализа). Дерево не построено; ниже только советы, основанные на доступных данных.\n")
	for i, recommendation := range recommendations {
		fmt.Fprintf(&message, "\n%d. %s", i+1, recommendation)
	}
	return message.String()
}

func shortRecommendationValue(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return value
}

func containsRecommendationMarker(value string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
