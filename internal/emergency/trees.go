package emergency

import (
	"fmt"
	"strings"

	"activity-descriptor/internal/domain"
)

// SourceID is the synthetic interview source used by the emergency tree.
const SourceID = "emergency-interview"

// The emergency trees deliberately use a fixed structure. They are a safe
// fallback for the user, not a replacement for the LLM analyst: every quote
// still comes from the real interview, and the run is marked degraded.
type taskSpec struct {
	title   string
	actions []string
}

type functionSpec struct {
	title string
	tasks []taskSpec
}

type treeSpec struct {
	roleTitle string
	roleDesc  string
	functions []functionSpec
}

var genericSpec = treeSpec{
	roleTitle: "Специалист",
	roleDesc:  "Роль, восстановленная аварийным детерминированным анализом.",
	functions: []functionSpec{
		{title: "Выполнение профессиональных задач", tasks: []taskSpec{
			{title: "Решение типовых задач", actions: []string{"Выполнить действие по задаче", "Проверить результат"}},
			{title: "Работа с инструментами", actions: []string{"Выбрать инструмент", "Применить инструмент"}},
		}},
		{title: "Взаимодействие с коллегами", tasks: []taskSpec{
			{title: "Согласование работы", actions: []string{"Обсудить задачу", "Зафиксировать договорённости"}},
			{title: "Передача результата", actions: []string{"Подготовить описание", "Передать результат"}},
		}},
		{title: "Контроль качества", tasks: []taskSpec{
			{title: "Проверка результата", actions: []string{"Проверить по критериям", "Исправить замечания"}},
			{title: "Улучшение процесса", actions: []string{"Найти потерю", "Предложить улучшение"}},
		}},
	},
}

var backendJuniorSpec = treeSpec{
	roleTitle: "Backend-разработчик (Junior)",
	roleDesc:  "Разрабатывает типовые серверные функции под руководством более опытных коллег.",
	functions: []functionSpec{
		{title: "Разработка серверной логики", tasks: []taskSpec{
			{title: "Реализация API-метода", actions: []string{"Написать обработчик запроса", "Обработать ошибки и коды ответа"}},
			{title: "Работа с базой данных", actions: []string{"Написать SQL-запрос", "Применить миграцию или ORM-модель"}},
		}},
		{title: "Тестирование и отладка", tasks: []taskSpec{
			{title: "Написание тестов", actions: []string{"Покрыть функцию тестом", "Запустить тесты и разобрать падение"}},
			{title: "Поиск дефектов", actions: []string{"Воспроизвести ошибку", "Исправить и проверить исправление"}},
		}},
		{title: "Командная работа", tasks: []taskSpec{
			{title: "Работа с репозиторием", actions: []string{"Создать ветку и коммит", "Пройти code review"}},
			{title: "Документирование изменений", actions: []string{"Описать изменение", "Обновить README или API-описание"}},
		}},
	},
}

var backendMiddleSpec = treeSpec{
	roleTitle: "Backend-разработчик (Middle)",
	roleDesc:  "Самостоятельно проектирует и развивает серверные сервисы, отвечает за интеграции и качество.",
	functions: []functionSpec{
		{title: "Проектирование сервисов", tasks: []taskSpec{
			{title: "Проектирование API", actions: []string{"Согласовать контракт API", "Описать ошибки и версионирование"}},
			{title: "Моделирование данных", actions: []string{"Спроектировать схему данных", "Подготовить миграции"}},
		}},
		{title: "Разработка и интеграции", tasks: []taskSpec{
			{title: "Интеграция с внешними сервисами", actions: []string{"Реализовать клиент", "Обработать таймауты и повторные вызовы"}},
			{title: "Оптимизация запросов", actions: []string{"Найти узкое место", "Добавить индекс или кэш"}},
		}},
		{title: "Качество и эксплуатация", tasks: []taskSpec{
			{title: "Тестирование", actions: []string{"Покрыть сценарии тестами", "Настроить проверки в CI"}},
			{title: "Наблюдаемость", actions: []string{"Добавить логи и метрики", "Разобрать инцидент"}},
		}},
	},
}

var backendSeniorSpec = treeSpec{
	roleTitle: "Backend-разработчик (Senior)",
	roleDesc:  "Отвечает за архитектуру, надёжность и техническое развитие нескольких сервисов.",
	functions: []functionSpec{
		{title: "Архитектура системы", tasks: []taskSpec{
			{title: "Проектирование системы", actions: []string{"Выбрать архитектурный стиль", "Определить границы сервисов"}},
			{title: "Техническая стратегия", actions: []string{"Оценить риски", "Спланировать эволюцию системы"}},
		}},
		{title: "Надёжность и масштабирование", tasks: []taskSpec{
			{title: "Отказоустойчивость", actions: []string{"Определить SLO", "Провести нагрузочное тестирование"}},
			{title: "Масштабирование", actions: []string{"Найти bottleneck", "Внедрить масштабирование"}},
		}},
		{title: "Техническое лидерство", tasks: []taskSpec{
			{title: "Развитие команды", actions: []string{"Провести design review", "Наставить разработчиков"}},
			{title: "Развитие процессов", actions: []string{"Улучшить CI/CD", "Внедрить инженерные стандарты"}},
		}},
	},
}

var dataAnalystSpec = treeSpec{
	roleTitle: "Аналитик данных",
	roleDesc:  "Собирает, проверяет и интерпретирует данные, превращая их в решения для бизнеса.",
	functions: []functionSpec{
		{title: "Сбор и подготовка данных", tasks: []taskSpec{
			{title: "Формирование выборки", actions: []string{"Определить источник данных", "Написать SQL-запрос"}},
			{title: "Проверка качества", actions: []string{"Проверить пропуски и дубли", "Сверить данные с бизнесом"}},
		}},
		{title: "Анализ и визуализация", tasks: []taskSpec{
			{title: "Исследование данных", actions: []string{"Рассчитать метрики", "Проверить гипотезу"}},
			{title: "Построение дашборда", actions: []string{"Собрать дашборд", "Проверить корректность показателей"}},
		}},
		{title: "Коммуникация результата", tasks: []taskSpec{
			{title: "Интерпретация", actions: []string{"Сформулировать выводы", "Оценить ограничения анализа"}},
			{title: "Передача результата", actions: []string{"Подготовить презентацию", "Защитить выводы перед заказчиком"}},
		}},
	},
}

var devOpsSpec = treeSpec{
	roleTitle: "DevOps-инженер",
	roleDesc:  "Отвечает за инфраструктуру, доставку изменений, наблюдаемость и устойчивость сервисов.",
	functions: []functionSpec{
		{title: "Управление инфраструктурой", tasks: []taskSpec{
			{title: "Подготовка окружений", actions: []string{"Описать инфраструктуру как код", "Развернуть окружение"}},
			{title: "Конфигурация", actions: []string{"Настроить сеть и секреты", "Проверить доступность"}},
		}},
		{title: "CI/CD и доставка", tasks: []taskSpec{
			{title: "Сборка и доставка", actions: []string{"Настроить пайплайн", "Автоматизировать деплой"}},
			{title: "Качество пайплайна", actions: []string{"Добавить проверки", "Ускорить сборку"}},
		}},
		{title: "Эксплуатация и инциденты", tasks: []taskSpec{
			{title: "Мониторинг", actions: []string{"Настроить метрики и алерты", "Проверить дашборды"}},
			{title: "Разбор инцидента", actions: []string{"Локализовать проблему", "Провести postmortem"}},
		}},
	},
}

// Tree builds one complete role -> function -> task -> action tree for the
// profession. It selects the Backend level from junior/middle/senior markers.
func Tree(profession string, session domain.InterviewSession) []domain.Node {
	spec := specFor(profession)
	nodes := make([]domain.Node, 0, 1+len(spec.functions)*7)
	quoteIndex := 0
	role := makeNode("emergency-role", domain.NodeRole, spec.roleTitle, spec.roleDesc, nil, nextQuote(session, &quoteIndex))
	nodes = append(nodes, role)
	for fi, function := range spec.functions {
		functionID := fmt.Sprintf("emergency-f%d", fi+1)
		functionNode := makeNode(functionID, domain.NodeFunction, function.title, function.title+".", &role.ID, nextQuote(session, &quoteIndex))
		nodes = append(nodes, functionNode)
		for ti, task := range function.tasks {
			taskID := fmt.Sprintf("%s-t%d", functionID, ti+1)
			taskNode := makeNode(taskID, domain.NodeTask, task.title, task.title+".", &functionNode.ID, nextQuote(session, &quoteIndex))
			nodes = append(nodes, taskNode)
			for ai, action := range task.actions {
				actionID := fmt.Sprintf("%s-a%d", taskID, ai+1)
				nodes = append(nodes, makeNode(actionID, domain.NodeAction, action, action+".", &taskNode.ID, nextQuote(session, &quoteIndex)))
			}
		}
	}
	return nodes
}

func specFor(profession string) treeSpec {
	normalized := strings.ToLower(strings.TrimSpace(profession))
	if strings.Contains(normalized, "backend") || strings.Contains(normalized, "бэкенд") || strings.Contains(normalized, "серверн") {
		switch {
		case strings.Contains(normalized, "junior") || strings.Contains(normalized, "младш"):
			return backendJuniorSpec
		case strings.Contains(normalized, "senior") || strings.Contains(normalized, "старш"):
			return backendSeniorSpec
		default:
			return backendMiddleSpec
		}
	}
	if strings.Contains(normalized, "devops") || strings.Contains(normalized, "девопс") {
		return devOpsSpec
	}
	if strings.Contains(normalized, "аналитик") || strings.Contains(normalized, "analyst") {
		return dataAnalystSpec
	}
	return genericSpec
}

func nextQuote(session domain.InterviewSession, index *int) string {
	i := *index
	*index = *index + 1
	if i < len(session.Answers) {
		if text := strings.TrimSpace(session.Answers[i].Text); text != "" {
			return text
		}
	}
	if i < len(session.Questions) {
		if text := strings.TrimSpace(session.Questions[i].Text); text != "" {
			return text
		}
	}
	if len(session.Questions) > 0 {
		return strings.TrimSpace(session.Questions[0].Text)
	}
	return "Ответ интервью сохранён."
}

func makeNode(id string, kind domain.NodeType, title, description string, parent *string, quote string) domain.Node {
	return domain.Node{
		ID: id, Type: kind, Title: title, Description: description, ParentID: parent,
		Evidence: []domain.Evidence{{
			ID:          "emergency-evidence-" + id,
			SourceID:    SourceID,
			RefID:       fmt.Sprintf("fallback-%s", id),
			Quote:       quote,
			Status:      domain.EvidencePending,
			ClaimOrigin: domain.ClaimSourceStatement,
		}},
	}
}
