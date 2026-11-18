package interviewer

import (
	"fmt"
	"strings"
)

// Course is one free Russian course recommended at the end of the interview.
// The catalogue is intentionally simple for the MVP: courses are selected by
// profession only, without analysing the transcript yet.
type Course struct {
	Title string
	URL   string
}

// CoursesForProfession returns the two free Stepik courses for a supported
// profession. It is deliberately deterministic: the interviewer sends this
// final message in the same chat after the pipeline has completed.
func CoursesForProfession(profession string) []Course {
	switch normalizeProfession(profession) {
	case "analyst":
		return []Course{
			{Title: "SQL с нуля: 7 модулей на одном наборе данных", URL: "https://stepik.org/course/300883/promo"},
			{Title: "Продвинутая подготовка данных для анализа в BI", URL: "https://stepik.org/course/266132/promo"},
		}
	case "devops":
		return []Course{
			{Title: "DevOps простым языком", URL: "https://stepik.org/course/227851/promo"},
			{Title: "Docker для начинающих + практический опыт", URL: "https://stepik.org/course/123300/promo"},
		}
	case "backend":
		return []Course{
			{Title: "Разработка веб-сервисов на Golang (Go)", URL: "https://stepik.org/course/187490/promo"},
			{Title: "Django Великолепный: бесплатный курс для начинающих", URL: "https://stepik.org/course/293404/promo"},
		}
	default:
		return nil
	}
}

// FinalCoursesMessage is the interviewer's closing message in the same chat.
// For now it uses the profession only; later it can be extended to use the
// assembled activity tree without adding another agent.
func FinalCoursesMessage(profession string) string {
	courses := CoursesForProfession(profession)
	if len(courses) == 0 {
		return "Интервью завершено. Спасибо за ответы! Итоговый анализ сохранён в дереве компетенций."
	}
	var b strings.Builder
	b.WriteString("Интервью завершено. Спасибо за ответы!\n\n")
	b.WriteString("Общий анализ сохранён в дереве компетенций. ")
	b.WriteString("Вот бесплатные курсы Stepik по вашей профессии:\n")
	for i, course := range courses {
		fmt.Fprintf(&b, "\n%d. %s\n%s", i+1, course.Title, course.URL)
	}
	return b.String()
}

func normalizeProfession(profession string) string {
	s := strings.ToLower(strings.ReplaceAll(profession, "ё", "е"))
	switch {
	case strings.Contains(s, "analyst"), strings.Contains(s, "аналитик"), strings.Contains(s, "данных"):
		return "analyst"
	case strings.Contains(s, "devops"), strings.Contains(s, "девопс"):
		return "devops"
	case strings.Contains(s, "backend"), strings.Contains(s, "бэкенд"), strings.Contains(s, "бекенд"), strings.Contains(s, "разработчик"):
		return "backend"
	default:
		return ""
	}
}
