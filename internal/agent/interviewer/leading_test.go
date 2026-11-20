package interviewer

import (
	"strings"
	"testing"

	"activity-descriptor/internal/domain"
)

func TestLooksLeading(t *testing.T) {
	tests := []struct {
		name     string
		question string
		want     bool
	}{
		{"вы ведь", "Вы ведь пишете SQL-запросы?", true},
		{"ведь вы", "Ведь вы отвечаете за отчётность?", true},
		{"правильно ли я понимаю", "Правильно ли я понимаю, что вы строите дашборды?", true},
		{"я правильно понимаю", "Я правильно понимаю, что вы работаете с PostgreSQL?", true},
		{"вы же", "Вы же сами настраивали CI/CD?", true},
		{"разве вы не", "Разве вы не пользуетесь Docker?", true},
		{"разве не вы", "Разве не вы отвечаете за релизы?", true},
		{"согласны ли вы", "Согласны ли вы, что это ваша задача?", true},
		{"подтвердите что", "Подтвердите, что вы писали этот запрос.", true},
		{"наверное вы", "Наверное, вы работали с Kubernetes?", true},
		{"вы наверняка", "Вы наверняка сталкивались с инцидентами?", true},
		{"как известно", "Как известно, вы отвечаете за отчётность.", true},
		{"не так ли", "Не так ли?", true},
		{"без вопросительного знака", "Вы ведь пишете SQL-запросы", true},
		{"нейтральный вопрос о роли", "Опишите, пожалуйста, чем вы занимаетесь в роли аналитика данных и за что отвечаете.", false},
		{"нейтральный вопрос о случае", "Приведите, пожалуйста, конкретный пример задачи, которую вы выполняли на прошлой неделе.", false},
		{"нейтральный вопрос об инструментах", "Какими инструментами и какими шагами вы пользуетесь, когда пишете SQL-запрос?", false},
		{"нейтральный вопрос о личном вкладе", "Что именно вы сделали сами, а что — команда?", false},
		{"нейтральный вопрос с отрицанием", "Почему вы выбрали PostgreSQL, а не MySQL?", false},
		{"английский нейтральный", "Do you use Docker?", false},
		{"английский вопрос", "How do you write SQL queries?", false},
		{"пустая строка", "", false},
		{"слово ведьма", "Вы ведьма?", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := LooksLeading(test.question); got != test.want {
				t.Fatalf("LooksLeading(%q) = %v, want %v", test.question, got, test.want)
			}
		})
	}
}

func TestHasProductJargon(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"дерево", "Опишите дерево деятельности.", true},
		{"дерева", "Какие ветки дерева вы заполнили?", true},
		{"дерево компетенций", "Расскажите про дерево компетенций.", true},
		{"верхний регистр", "ДЕРЕВО уже готово?", true},
		{"обоснование", "Дайте обоснование этого действия.", true},
		{"обоснование во множественном числе", "Соберите обоснования по задачам.", true},
		{"evidence", "Приведите evidence по этой задаче.", true},
		{"evidence в верхнем регистре", "Покажите EVIDENCE.", true},
		{"KPI", "Какой KPI у вашей команды?", true},
		{"KPI в смешанном регистре", "Kpi за квартал.", true},
		{"KPI во множественном числе", "Какие KPIs вы отслеживаете?", true},
		{"таксономия", "Опишите таксономию деятельности.", true},
		{"английский KPI", "What is your KPI?", true},
		{"без знака вопроса", "Дерево", true},
		{"нейтральный вопрос о роли", "Опишите, пожалуйста, чем вы занимаетесь в роли аналитика данных и за что отвечаете.", false},
		{"метрики вместо KPI", "Какие метрики вы отслеживаете?", false},
		{"нейтральный вопрос о задаче", "Приведите пример задачи и результата.", false},
		{"нейтральный вопрос об инструментах", "Какие инструменты вы используете?", false},
		{"английский нейтральный", "Tell me about your role.", false},
		{"слово деревянный", "У вас деревянный стол?", false},
		{"пустая строка", "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := HasProductJargon(test.text); got != test.want {
				t.Fatalf("HasProductJargon(%q) = %v, want %v", test.text, got, test.want)
			}
		})
	}
}

func TestFallbackQuestionsAvoidLeadingFormulationsAndJargon(t *testing.T) {
	levels := []domain.QuestionLevel{
		domain.QuestionRole,
		domain.QuestionFunction,
		domain.QuestionTask,
		domain.QuestionAction,
		domain.QuestionLevel("unknown"),
	}
	professions := []string{"", "Аналитик данных", "DevOps-инженер", "Backend-разработчик"}
	for _, level := range levels {
		for _, profession := range professions {
			question := FallbackQuestion(level, profession)
			if LooksLeading(question.Text) {
				t.Errorf("FallbackQuestion(%q, %q) is leading: %q", level, profession, question.Text)
			}
			if HasProductJargon(question.Text) {
				t.Errorf("FallbackQuestion(%q, %q) contains product jargon: %q", level, profession, question.Text)
			}
		}
	}
}

func TestClarificationQuestionQuotesAnswerVerbatim(t *testing.T) {
	answer := "  Ну, всякое бывало, по-разному  "
	question := ClarificationQuestion(answer)
	if !strings.Contains(question.Text, "«Ну, всякое бывало, по-разному»") {
		t.Fatalf("clarification does not quote the answer verbatim: %q", question.Text)
	}
	if !question.Clarification || !question.Fallback {
		t.Fatalf("clarification flags = clarification %v, fallback %v; want both true",
			question.Clarification, question.Fallback)
	}
}
