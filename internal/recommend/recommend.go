// Package recommend builds deterministic post-pipeline recommendations:
// free Stepik courses and a personal development plan for a finished run.
//
// The package performs no LLM or network calls. The role is derived from the
// run profession string, the level is taken from explicit clues in the same
// string and, when no clue is present, adjusted with the number of collected
// answers and the size of the competency tree. All user-facing texts are in
// Russian.
package recommend

import (
	"strconv"
	"strings"
	"time"

	"activity-descriptor/internal/domain"
)

// Supported role identifiers. They are exported for tests and callers that
// want to branch on the detected role.
const (
	RoleDataAnalyst = "data_analyst"
	RoleDevOps      = "devops"
	RoleBackend     = "backend"
	RoleUnknown     = "unknown"
)

// Detected experience levels stored in Recommendation.Level.
const (
	LevelJunior  = "junior"
	LevelMiddle  = "middle"
	LevelSenior  = "senior"
	LevelUnknown = "unknown"
)

const (
	sqlCourseTitle    = "SQL с нуля: 7 модулей на одном наборе данных"
	sqlCourseURL      = "https://stepik.org/course/300883/promo"
	biCourseTitle     = "Продвинутая подготовка данных для анализа в BI"
	biCourseURL       = "https://stepik.org/course/266132/promo"
	devopsCourseTitle = "DevOps простым языком"
	devopsCourseURL   = "https://stepik.org/course/227851/promo"
	dockerCourseTitle = "Docker для начинающих + практический опыт"
	dockerCourseURL   = "https://stepik.org/course/123300/promo"
	goCourseTitle     = "Разработка веб-сервисов на Golang (Go)"
	goCourseURL       = "https://stepik.org/course/187490/promo"
	djangoCourseTitle = "Django Великолепный: бесплатный курс для начинающих"
	djangoCourseURL   = "https://stepik.org/course/293404/promo"

	stepikProvider = "Stepik"
)

// Recommendation is the deterministic result of the post-pipeline analysis:
// two free Stepik courses and a personal development plan tailored to the
// run's profession, level and competency tree.
type Recommendation struct {
	Profession  string     `json:"profession"`
	Level       string     `json:"level"`
	Summary     string     `json:"summary"`
	Courses     []Course   `json:"courses"`
	Plan        []PlanStep `json:"plan"`
	GeneratedAt time.Time  `json:"generated_at"`
}

// Course is one free online course recommended to the respondent.
type Course struct {
	Title    string `json:"title"`
	URL      string `json:"url"`
	Provider string `json:"provider"`
	Free     bool   `json:"free"`
	Reason   string `json:"reason"`
}

// PlanStep is one step of the personal development plan. Steps are ordered
// from 1 to len(plan); Done starts as false and is intended for the UI
// checklist.
type PlanStep struct {
	Order       int    `json:"order"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Done        bool   `json:"done"`
}

// courseTemplate describes one catalogue entry; basics marks the entry-level
// course that junior respondents should start with.
type courseTemplate struct {
	title    string
	url      string
	focus    string
	basics   bool
	keywords []string
}

var roleCourses = map[string][]courseTemplate{
	RoleDataAnalyst: {
		{
			title:  sqlCourseTitle,
			url:    sqlCourseURL,
			focus:  "базовый SQL: SELECT, JOIN и GROUP BY на одном наборе данных",
			basics: true,
			keywords: []string{
				"sql", "запрос", "postgres", "mysql", "бд",
			},
		},
		{
			title:  biCourseTitle,
			url:    biCourseURL,
			focus:  "подготовка данных и визуализация в BI",
			basics: false,
			keywords: []string{
				"bi", "tableau", "визуализ", "дашборд", "отчет", "отчёт", "excel", "метрик",
			},
		},
	},
	RoleDevOps: {
		{
			title:  devopsCourseTitle,
			url:    devopsCourseURL,
			focus:  "основы DevOps: Linux, CI/CD и автоматизация",
			basics: true,
			keywords: []string{
				"devops", "девопс", "linux", "ci/cd", "ansible", "terraform", "kubernetes", "k8s", "мониторинг",
			},
		},
		{
			title:  dockerCourseTitle,
			url:    dockerCourseURL,
			focus:  "контейнеризация и практический опыт с Docker",
			basics: false,
			keywords: []string{
				"docker", "докер", "контейнер",
			},
		},
	},
	RoleBackend: {
		{
			title:  djangoCourseTitle,
			url:    djangoCourseURL,
			focus:  "основы веб-разработки на Django",
			basics: true,
			keywords: []string{
				"django", "flask", "python", "web", "веб", "html", "http",
			},
		},
		{
			title:  goCourseTitle,
			url:    goCourseURL,
			focus:  "разработка веб-сервисов на Go",
			basics: false,
			keywords: []string{
				"golang", "go-разработ", "go разработ", "api", "rest", "grpc", "микросервис",
			},
		},
	},
}

// Build returns deterministic recommendations for a completed run.
//
// nodes is the run's competency tree (may be empty for degraded runs) and
// answers are the collected evidence quotes (may be nil when the caller could
// not load them). Build never returns an error: it degrades to a plan based on
// the profession and tree only.
func Build(profession string, nodes []domain.Node, answers []domain.Evidence) Recommendation {
	profession = strings.TrimSpace(profession)
	role := DetectRole(profession)
	level := DetectLevel(profession, nodes, answers)
	gaps := detectGaps(nodes)
	courses := coursesFor(role, level, gaps)
	plan := planFor(profession, role, level, gaps, courses)

	return Recommendation{
		Profession: profession,
		Level:      level,
		Summary:    summaryFor(profession, role, level, len(nodes), gaps, len(plan)),
		Courses:    courses,
		Plan:       plan,
		// GeneratedAt is the only non-deterministic field: it records when the
		// recommendation was produced. All content above is a pure function of
		// the inputs.
		GeneratedAt: time.Now().UTC(),
	}
}

// DetectRole maps a free-form profession string to one of the supported roles.
// Matching is case-insensitive and accepts Russian and English spellings.
func DetectRole(profession string) string {
	s := normalize(profession)
	switch {
	case containsAny(s, "devops", "dev ops", "девопс", "sre"):
		return RoleDevOps
	case containsAny(s, "analyst", "аналитик", "данных", "analytics"):
		return RoleDataAnalyst
	case containsAny(s, "backend", "back-end", "back end", "бэкенд", "бекенд", "разработчик", "developer"):
		return RoleBackend
	default:
		return RoleUnknown
	}
}

// DetectLevel returns the experience level of the respondent.
//
// Explicit clues in the profession string win over heuristics. When no clue is
// present, a run with at most two answers or at most three tree nodes is
// treated as junior. A run with at least eight tree nodes and at least eight
// pieces of evidence is treated as middle; tree evidence is used as a fallback
// proxy when the caller could not load the answers separately.
func DetectLevel(profession string, nodes []domain.Node, answers []domain.Evidence) string {
	s := normalize(profession)
	switch {
	case containsAny(s, "senior", "сеньор", "ведущ", "lead", "руковод", "principal", "старший"):
		return LevelSenior
	case containsAny(s, "middle", "средн", "мидл"):
		return LevelMiddle
	case containsAny(s, "junior", "джун", "начинающ", "стажер", "1-3", "без опыта", "no experience"):
		return LevelJunior
	}

	if len(answers) <= 2 || len(nodes) <= 3 {
		return LevelJunior
	}

	evidenceCount := len(answers)
	nodeEvidence := 0
	for _, node := range nodes {
		nodeEvidence += len(node.Evidence)
	}
	if nodeEvidence > evidenceCount {
		evidenceCount = nodeEvidence
	}
	if len(nodes) >= 8 && evidenceCount >= 8 {
		return LevelMiddle
	}
	return LevelUnknown
}

// coursesFor selects exactly two free Stepik courses for a known role. Junior
// and unknown levels start with the entry-level course; middle and senior
// levels get the advanced course first.
func coursesFor(role, level string, gaps []string) []Course {
	templates := roleCourses[role]
	if len(templates) == 0 {
		// Unknown role: do not attach unsupported courses, only a safe plan.
		return []Course{}
	}

	ordered := make([]courseTemplate, len(templates))
	copy(ordered, templates)
	if isAdvanced(level) && len(ordered) > 1 {
		ordered[0], ordered[1] = ordered[1], ordered[0]
	}

	courses := make([]Course, 0, len(ordered))
	for _, template := range ordered {
		courses = append(courses, Course{
			Title:    template.title,
			URL:      template.url,
			Provider: stepikProvider,
			Free:     true,
			Reason:   courseReason(level, template, gaps),
		})
	}
	return courses
}

// courseReason explains why a course is useful, referencing the detected level
// and (when present) a concrete gap from the tree.
func courseReason(level string, template courseTemplate, gaps []string) string {
	var b strings.Builder
	switch level {
	case LevelJunior:
		if template.basics {
			b.WriteString("Уровень junior: начать с базы — ")
		} else {
			b.WriteString("Уровень junior: следующий шаг после базы — ")
		}
	case LevelMiddle:
		if template.basics {
			b.WriteString("Уровень middle: освежить базу — ")
		} else {
			b.WriteString("Уровень middle: углубить навык — ")
		}
	case LevelSenior:
		if template.basics {
			b.WriteString("Уровень senior: системно закрепить базу — ")
		} else {
			b.WriteString("Уровень senior: расширить экспертную практику — ")
		}
	default:
		if template.basics {
			b.WriteString("Уровень не определён: безопасный старт с базы — ")
		} else {
			b.WriteString("Уровень не определён: продолжение после базы — ")
		}
	}
	b.WriteString(template.focus)
	b.WriteString(".")

	switch gap := matchGap(gaps, template.keywords); {
	case gap != "":
		b.WriteString(" Пробел в дереве компетенций: «" + gap + "».")
	case len(gaps) > 0:
		b.WriteString(" Закрывает пробелы из персонального дерева компетенций.")
	default:
		b.WriteString(" Опирается на дерево компетенций прогона.")
	}
	return b.String()
}

// matchGap returns the first tree gap whose title mentions any of the course
// keywords, or an empty string when nothing matches.
func matchGap(gaps, keywords []string) string {
	for _, gap := range gaps {
		lower := strings.ToLower(gap)
		for _, keyword := range keywords {
			if strings.Contains(lower, keyword) {
				return gap
			}
		}
	}
	return ""
}

// planDraft is a plan step before ordering is assigned.
type planDraft struct {
	title       string
	description string
}

// planFor builds 4-6 concrete Russian-language steps for the detected role and
// level. Known roles always reference their two selected courses.
func planFor(profession, role, level string, gaps []string, courses []Course) []PlanStep {
	switch role {
	case RoleDataAnalyst:
		return buildPlan(analystPlan(level, gaps, courses))
	case RoleDevOps:
		return buildPlan(devopsPlan(level, gaps, courses))
	case RoleBackend:
		return buildPlan(backendPlan(level, gaps, courses))
	default:
		return buildPlan(genericPlan(profession, gaps))
	}
}

func analystPlan(level string, gaps []string, courses []Course) []planDraft {
	sql := resourceOr(courses, sqlCourseURL, "курс по SQL")
	bi := resourceOr(courses, biCourseURL, "курс по подготовке данных и BI")

	var drafts []planDraft
	if isAdvanced(level) {
		drafts = append(drafts,
			planDraft{
				title:       "Углубить подготовку данных в BI",
				description: "Пройдите " + bi + " и соберите дашборд по рабочим метрикам: от сырых данных до наглядных выводов.",
			},
			planDraft{
				title:       "Закрепить продвинутый SQL",
				description: "Пройдите " + sql + " и отработайте оконные функции, CTE и оптимизацию запросов на одном наборе данных.",
			},
			planDraft{
				title:       "Собрать портфолио из трёх end-to-end кейсов",
				description: "Опишите три кейса по схеме «бизнес-вопрос → данные → проверка гипотезы → измеримый вывод»; приложите запросы и визуализации.",
			},
		)
	} else {
		drafts = append(drafts,
			planDraft{
				title:       "Освоить базовый SQL",
				description: "Пройдите " + sql + " и отработайте SELECT, JOIN и GROUP BY на одном наборе данных — это база аналитика.",
			},
			planDraft{
				title:       "Перейти к подготовке данных и визуализации",
				description: "Пройдите " + bi + " и соберите первый дашборд по учебному набору данных.",
			},
			planDraft{
				title:       "Собрать портфолио из трёх аналитических отчётов",
				description: "Опишите три кейса: вопрос, данные, проверка гипотезы и вывод; приложите запросы и визуализации.",
			},
		)
	}
	return append(drafts, planTail(level, gaps)...)
}

func devopsPlan(level string, gaps []string, courses []Course) []planDraft {
	devops := resourceOr(courses, devopsCourseURL, "курс по основам DevOps")
	docker := resourceOr(courses, dockerCourseURL, "курс по Docker")

	var drafts []planDraft
	if isAdvanced(level) {
		drafts = append(drafts,
			planDraft{
				title:       "Углубить контейнеризацию",
				description: "Пройдите " + docker + " и переведите pet-проект на несколько контейнеров с docker compose.",
			},
			planDraft{
				title:       "Систематизировать DevOps-практики",
				description: "Пройдите " + devops + " и опишите процесс доставки изменений от коммита до продакшена.",
			},
			planDraft{
				title:       "Спроектировать отказоустойчивый стенд",
				description: "Добавьте мониторинг, резервное копирование и проверку деградации для учебного сервиса.",
			},
		)
	} else {
		drafts = append(drafts,
			planDraft{
				title:       "Освоить основы DevOps",
				description: "Пройдите " + devops + " и разберитесь в Linux, CI/CD и автоматизации.",
			},
			planDraft{
				title:       "Научиться контейнеризации",
				description: "Пройдите " + docker + " и запустите собственный сервис в контейнере.",
			},
			planDraft{
				title:       "Поднять домашнюю лабораторию",
				description: "Соберите стенд: Linux-виртуальная машина, Docker и пайплайн CI/CD для pet-проекта.",
			},
		)
	}
	return append(drafts, planTail(level, gaps)...)
}

func backendPlan(level string, gaps []string, courses []Course) []planDraft {
	goCourse := resourceOr(courses, goCourseURL, "курс по разработке на Go")
	django := resourceOr(courses, djangoCourseURL, "курс по Django")

	var drafts []planDraft
	if isAdvanced(level) {
		drafts = append(drafts,
			planDraft{
				title:       "Углубить разработку на Go",
				description: "Пройдите " + goCourse + " и реализуйте API с обработкой ошибок, валидацией запросов и понятной структурой пакетов.",
			},
			planDraft{
				title:       "Освежить веб-фреймворки",
				description: "Пройдите " + django + " и сравните подходы к маршрутизации, ORM и миграциям на своём проекте.",
			},
			planDraft{
				title:       "Спроектировать сервис под нагрузку",
				description: "Продумайте схему данных, кэширование и обработку ошибок для учебного сервиса, опишите решения в README.",
			},
		)
	} else {
		drafts = append(drafts,
			planDraft{
				title:       "Освоить основы веб-разработки",
				description: "Пройдите " + django + " и соберите простое веб-приложение с формами и базой данных.",
			},
			planDraft{
				title:       "Перейти к веб-сервисам на Go",
				description: "Пройдите " + goCourse + " и напишите HTTP-обработчики для своего приложения.",
			},
			planDraft{
				title:       "Написать и задеплоить первый REST API",
				description: "Реализуйте CRUD-сервис с базой данных и тестами, запустите его локально или в контейнере.",
			},
		)
	}
	return append(drafts, planTail(level, gaps)...)
}

func genericPlan(profession string, gaps []string) []planDraft {
	label := "не указана"
	if profession != "" {
		label = profession
	}
	return []planDraft{
		{
			title:       "Уточнить профессию и целевую роль",
			description: "Профессия «" + label + "» не сопоставлена с поддерживаемыми ролями. Выберите направление: аналитик данных, DevOps или backend-разработчик.",
		},
		{
			title:       "Зафиксировать текущий уровень",
			description: "Опирайтесь на дерево компетенций прогона. " + gapHint(gaps),
		},
		{
			title:       "Сформулировать 2–3 измеримые цели на месяц",
			description: "Например: закрыть конкретный узел дерева, собрать портфолио-кейс и подтвердить навык на практике.",
		},
		{
			title:       "Подобрать бесплатные материалы под подтверждённую роль",
			description: "Курсы не прикладываются, пока роль не распознана: без подтверждённого направления рекомендации могут быть неточными. Уточните профессию и перезапустите анализ.",
		},
		{
			title:       "Проверять прогресс раз в две недели",
			description: "Отмечайте выполненные шаги в плане и обновляйте цели: рекомендации детерминированные, поэтому прогресс виден по чек-листу.",
		},
	}
}

// planTail adds the role-independent closing steps; seniors get one extra
// mentoring step so their plan stays within the 4-6 step range.
func planTail(level string, gaps []string) []planDraft {
	steps := []planDraft{
		{
			title:       "Закрыть пробелы из дерева компетенций",
			description: "Сверьтесь с деревом прогона и выберите 2–3 узла для развития. " + gapHint(gaps),
		},
		{
			title:       "Проверить результат на реальной задаче",
			description: "Примените изученное в рабочей или учебной задаче и сохраните результат в портфолио: это подтвердит рост уровня.",
		},
	}
	if level == LevelSenior {
		steps = append(steps, planDraft{
			title:       "Передать опыт команде",
			description: "Проведите внутренний воркшоп или код-ревью по темам курсов и оформите чек-лист лучших практик.",
		})
	}
	return steps
}

// buildPlan numbers drafts and fills the checklist flag.
func buildPlan(drafts []planDraft) []PlanStep {
	steps := make([]PlanStep, 0, len(drafts))
	for i, draft := range drafts {
		steps = append(steps, PlanStep{
			Order:       i + 1,
			Title:       draft.title,
			Description: draft.description,
			Done:        false,
		})
	}
	return steps
}

// summaryFor builds the human-readable Russian summary of the recommendation.
func summaryFor(profession, role, level string, nodeCount int, gaps []string, planLen int) string {
	var b strings.Builder

	if role == RoleUnknown {
		label := "не указана"
		if profession != "" {
			label = profession
		}
		b.WriteString("Профессия «" + label + "» не сопоставлена с поддерживаемыми ролями (аналитик данных, DevOps, backend-разработчик). ")
		b.WriteString("Ниже универсальный план профессионального развития без курсов: сначала уточните роль, затем перезапустите анализ. ")
	} else {
		b.WriteString("Персональные рекомендации для роли «" + roleLabel(role) + "» (уровень: " + levelLabel(level) + "): ")
		b.WriteString(strconv.Itoa(planLen) + " шагов развития и 2 бесплатных курса Stepik. ")
	}

	switch {
	case len(gaps) > 0:
		b.WriteString("Пробелы в дереве компетенций: " + quotedList(gaps, 3) + ".")
	case nodeCount == 0:
		b.WriteString("Дерево компетенций пустое: план собран по профессии.")
	default:
		b.WriteString("Явных пробелов в дереве компетенций нет: план закрепляет и расширяет текущий уровень.")
	}
	return b.String()
}

// detectGaps returns up to five competency gaps. Nodes without evidence are
// reported first (unverified competencies), then leaf tasks and actions that
// describe concrete skills to develop.
func detectGaps(nodes []domain.Node) []string {
	const maxGaps = 5

	gaps := make([]string, 0, maxGaps)
	seen := make(map[string]bool, maxGaps)
	add := func(title string) {
		title = strings.TrimSpace(title)
		if title == "" || len(gaps) >= maxGaps {
			return
		}
		key := strings.ToLower(title)
		if seen[key] {
			return
		}
		seen[key] = true
		gaps = append(gaps, title)
	}

	for _, node := range nodes {
		if len(node.Evidence) == 0 {
			add(node.Title)
		}
	}

	if len(gaps) < maxGaps {
		children := make(map[string]int, len(nodes))
		for _, node := range nodes {
			if node.ParentID != nil {
				children[*node.ParentID]++
			}
		}
		for _, node := range nodes {
			if node.Type != domain.NodeTask && node.Type != domain.NodeAction {
				continue
			}
			if children[node.ID] == 0 {
				add(node.Title)
			}
		}
	}
	return gaps
}

// resource renders a course as a plan resource line.
func resource(course Course) string {
	return "«" + course.Title + "» (" + course.Provider + ", бесплатно): " + course.URL
}

// resourceOr returns the rendered course with the given URL or a fallback
// phrase when the course is not in the selected pair.
func resourceOr(courses []Course, url, fallback string) string {
	for _, course := range courses {
		if course.URL == url {
			return resource(course)
		}
	}
	return fallback
}

// gapHint describes which gaps to start with as a standalone sentence.
func gapHint(gaps []string) string {
	if len(gaps) == 0 {
		return "Ориентируйтесь на узлы дерева, по которым пока нет уверенных доказательств."
	}
	return "В первую очередь закройте: " + quotedList(gaps, 3) + "."
}

// quotedList renders up to max items as «a», «b», «c».
func quotedList(items []string, max int) string {
	if len(items) > max {
		items = items[:max]
	}
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		quoted = append(quoted, "«"+item+"»")
	}
	return strings.Join(quoted, ", ")
}

// roleLabel returns the Russian display name of a role.
func roleLabel(role string) string {
	switch role {
	case RoleDataAnalyst:
		return "Аналитик данных"
	case RoleDevOps:
		return "DevOps"
	case RoleBackend:
		return "Backend-разработчик"
	default:
		return "не определена"
	}
}

// levelLabel returns the Russian display name of a level.
func levelLabel(level string) string {
	switch level {
	case LevelJunior, LevelMiddle, LevelSenior:
		return level
	default:
		return "не определён"
	}
}

// isAdvanced reports whether the level should start with the advanced course.
func isAdvanced(level string) bool {
	return level == LevelMiddle || level == LevelSenior
}

// normalize lowercases the text and unifies ё/dashes so substring matching is
// predictable.
func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ё", "е")
	s = strings.ReplaceAll(s, "–", "-")
	s = strings.ReplaceAll(s, "—", "-")
	return s
}

// containsAny reports whether s contains any of the needles.
func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
