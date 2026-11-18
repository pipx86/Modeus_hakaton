package api

import (
	"strings"
	"unicode"

	"activity-descriptor/internal/domain"
)

// Автоматические предложения ЗУН (mentor feature #10 "Auto-ZUN").
//
// Правила детерминированные и НЕ используют LLM: один и тот же узел с тем же
// деревом и профессией всегда даёт один и тот же набор предложений. Это
// осознанное ограничение: предложение должно быть объяснимым и проверяемым,
// а не ещё одним «чёрным ящиком». Модель (enrichZun) по-прежнему может
// заполнить ЗУН при финализации; эти правила предлагают значения только для
// узлов, где ЗУН пуст.
//
// Источники текста для правил:
//   - node.title и node.description;
//   - кавычки evidence (это подтверждённые цитаты источников/интервью);
//   - profession прогона (контекст предметной области).
//
// Категории правил:
//   - knowledge — «стек»/инструменты/домены: SQL, PostgreSQL, Excel, 1С,
//     Docker и т. п. Ключ с суффиксом "*" сравнивается по префиксу токена
//     (нужно для русской морфологии: «запрос*» -> «запросы», «запросами»);
//     ключ без "*" — точное совпадение токена;
//   - skills — действия и трудовые операции: глаголы/отглагольные
//     существительные (анализировать, готовить отчёты, планировать,
//     контролировать, вести переговоры);
//   - abilities — требования к исполнителю и ответственности из описаний
//     вакансий/интервью («ответственность», «в команде», «самостоятельно»,
//     «клиентоориентированность»).
//
// Токенизация: нижний регистр, ё->е, разделители — всё, что не буква и не
// цифра. Найденные пункты идут в фиксированном порядке правил и дедуплицируются.
// Если по тексту не нашлось ни одного правила, используется детерминированный
// fallback от типа узла и профессии, чтобы предложение никогда не было пустым.

const (
	// zunSuggestionSourceRule — метка источника предложения в DTO. Значение
	// фиксировано: фронтенд показывает его как «правило», а не «ИИ».
	zunSuggestionSourceRule = "rule"
	// zunSuggestionMaxPerList ограничивает каждый список ЗУН. 4 пункта — тот же
	// лимит, который просит модель enrichZun, поэтому UI не отличается по объёму.
	zunSuggestionMaxPerList = 4
	// zunSuggestionMaxItemRunes ограничивает один пункт ЗУН при сохранении.
	zunSuggestionMaxItemRunes = 200
	// zunSuggestionMaxItemsPerList ограничивает количество принимаемых пунктов.
	zunSuggestionMaxItemsPerList = 50
	// zunRuleEvidenceRunes ограничивает вклад одной цитаты в текст правил.
	zunRuleEvidenceRunes = 400
)

// zunRule — одно правило вывода: набор ключей (синонимов) и готовый пункт ЗУН.
// Правила проверяются в порядке объявления; порядок — часть детерминированного
// контракта.
type zunRule struct {
	keys []string
	hint string
}

// zunKnowledgeRules: инструменты, технологии и предметные области.
var zunKnowledgeRules = []zunRule{
	{keys: []string{"sql"}, hint: "SQL-запросы и реляционные базы данных"},
	{keys: []string{"postgres*", "postgre*", "pgsql"}, hint: "PostgreSQL"},
	{keys: []string{"mysql"}, hint: "MySQL"},
	{keys: []string{"oracle"}, hint: "Oracle Database"},
	{keys: []string{"clickhouse"}, hint: "ClickHouse"},
	{keys: []string{"mongodb", "mongo*"}, hint: "MongoDB"},
	{keys: []string{"python"}, hint: "Python: скрипты и обработка данных"},
	{keys: []string{"pandas"}, hint: "pandas"},
	{keys: []string{"numpy"}, hint: "NumPy"},
	{keys: []string{"javascript"}, hint: "JavaScript"},
	{keys: []string{"typescript"}, hint: "TypeScript"},
	{keys: []string{"java"}, hint: "Java"},
	{keys: []string{"kotlin"}, hint: "Kotlin"},
	{keys: []string{"golang"}, hint: "Go"},
	{keys: []string{"react"}, hint: "React"},
	{keys: []string{"vue"}, hint: "Vue.js"},
	{keys: []string{"angular"}, hint: "Angular"},
	{keys: []string{"docker"}, hint: "Docker и контейнеризация"},
	{keys: []string{"kubernetes", "k8s"}, hint: "Kubernetes"},
	{keys: []string{"git", "github", "gitlab"}, hint: "Git: ветвление и код-ревью"},
	{keys: []string{"linux"}, hint: "Linux"},
	{keys: []string{"excel"}, hint: "Excel: формулы и сводные таблицы"},
	{keys: []string{"1с", "1c"}, hint: "1С: учёт и отчётность"},
	{keys: []string{"sap"}, hint: "SAP"},
	{keys: []string{"crm"}, hint: "CRM-системы"},
	{keys: []string{"bitrix"}, hint: "Битрикс24"},
	{keys: []string{"jira"}, hint: "Jira"},
	{keys: []string{"confluence"}, hint: "Confluence"},
	{keys: []string{"agile"}, hint: "Agile-практики"},
	{keys: []string{"scrum"}, hint: "Scrum"},
	{keys: []string{"kanban"}, hint: "Kanban"},
	{keys: []string{"api", "rest"}, hint: "REST API"},
	{keys: []string{"json", "xml"}, hint: "Обмен данными: JSON/XML"},
	{keys: []string{"tableau"}, hint: "Tableau"},
	{keys: []string{"power bi"}, hint: "Power BI"},
	{keys: []string{"grafana"}, hint: "Grafana"},
	{keys: []string{"prometheus"}, hint: "Prometheus"},
	{keys: []string{"метрик*", "kpi"}, hint: "Метрики и KPI"},
	{keys: []string{"статистик*"}, hint: "Статистический анализ"},
	{keys: []string{"воронк*"}, hint: "Анализ воронки продаж"},
	{keys: []string{"дашборд*"}, hint: "Дашборды и визуализация данных"},
	{keys: []string{"визуализ*"}, hint: "Визуализация данных"},
	{keys: []string{"бюдж*"}, hint: "Бюджетирование"},
	{keys: []string{"налог*"}, hint: "Налоговый учёт"},
	{keys: []string{"бухгалт*"}, hint: "Бухгалтерский учёт"},
	{keys: []string{"аудит*"}, hint: "Аудит и внутренний контроль"},
	{keys: []string{"договор*"}, hint: "Договорная работа"},
	{keys: []string{"гост*"}, hint: "Нормативные документы и ГОСТ"},
	{keys: []string{"охрана труда"}, hint: "Охрана труда и техника безопасности"},
	{keys: []string{"менеджмент*"}, hint: "Управление и менеджмент"},
	{keys: []string{"маркетинг*"}, hint: "Маркетинг"},
	{keys: []string{"право", "правов*"}, hint: "Правовые основы профессиональной деятельности"},
	{keys: []string{"английск*"}, hint: "Английский язык"},
	{keys: []string{"документооборот*"}, hint: "Документооборот"},
	{keys: []string{"проектн*"}, hint: "Проектное управление"},
	{keys: []string{"риск*"}, hint: "Управление рисками"},
	{keys: []string{"финанс*"}, hint: "Финансовый анализ"},
	{keys: []string{"персонал*"}, hint: "Управление персоналом"},
	{keys: []string{"оборудован*"}, hint: "Оборудование и технологические процессы"},
	{keys: []string{"безопасн*"}, hint: "Безопасность и охрана труда"},
}

// zunSkillRules: действия, которые нужно уметь выполнять.
var zunSkillRules = []zunRule{
	{keys: []string{"анализ*", "аналитик*"}, hint: "Анализировать данные и требования"},
	{keys: []string{"sql", "запрос*"}, hint: "Писать SQL-запросы"},
	{keys: []string{"отчет*", "отчетн*"}, hint: "Готовить отчётность"},
	{keys: []string{"презентац*", "презент*"}, hint: "Готовить презентации и защищать результаты"},
	{keys: []string{"переговор*"}, hint: "Вести переговоры"},
	{keys: []string{"консульт*"}, hint: "Консультировать клиентов и коллег"},
	{keys: []string{"обуч*", "наставн*"}, hint: "Обучать и наставлять сотрудников"},
	{keys: []string{"контрол*", "провер*"}, hint: "Контролировать качество и сроки"},
	{keys: []string{"план*", "планир*"}, hint: "Планировать работу и ресурсы"},
	{keys: []string{"организ*"}, hint: "Организовывать процессы и работу"},
	{keys: []string{"координ*"}, hint: "Координировать участников"},
	{keys: []string{"разработ*", "созда*"}, hint: "Разрабатывать решения"},
	{keys: []string{"внедр*"}, hint: "Внедрять изменения и инструменты"},
	{keys: []string{"оптимиз*"}, hint: "Оптимизировать процессы"},
	{keys: []string{"документ*", "оформ*"}, hint: "Оформлять документацию"},
	{keys: []string{"соглас*"}, hint: "Согласовывать решения с заинтересованными сторонами"},
	{keys: []string{"продаж*", "продав*"}, hint: "Вести продажи"},
	{keys: []string{"закуп*"}, hint: "Организовывать закупки"},
	{keys: []string{"моделир*"}, hint: "Строить модели"},
	{keys: []string{"визуализ*"}, hint: "Визуализировать результаты"},
	{keys: []string{"тестир*"}, hint: "Тестировать решения"},
	{keys: []string{"поддерж*", "сопровожд*"}, hint: "Поддерживать пользователей и системы"},
	{keys: []string{"исслед*"}, hint: "Проводить исследования"},
	{keys: []string{"настро*"}, hint: "Настраивать инструменты и процессы"},
	{keys: []string{"формир*"}, hint: "Формировать отчёты и реестры"},
	{keys: []string{"управл*", "руковод*"}, hint: "Управлять командой и процессами"},
	{keys: []string{"коммуник*"}, hint: "Выстраивать коммуникации"},
	{keys: []string{"общен*"}, hint: "Взаимодействовать с коллегами и клиентами"},
}

// zunAbilityRules: требования к исполнителю и его ответственности.
var zunAbilityRules = []zunRule{
	{keys: []string{"ответственн*", "ответственность"}, hint: "Ответственность за результат"},
	{keys: []string{"команд*"}, hint: "Работа в команде"},
	{keys: []string{"самостоятельн*"}, hint: "Самостоятельность в принятии решений"},
	{keys: []string{"коммуникац*"}, hint: "Эффективная коммуникация"},
	{keys: []string{"многозадачн*"}, hint: "Работа в условиях многозадачности"},
	{keys: []string{"дедлайн*", "срок*"}, hint: "Ориентация на сроки"},
	{keys: []string{"клиент*"}, hint: "Клиентоориентированность"},
	{keys: []string{"внимательн*", "точность", "точн*"}, hint: "Внимательность к деталям"},
	{keys: []string{"стресс*"}, hint: "Стрессоустойчивость"},
	{keys: []string{"руковод*", "управл*"}, hint: "Управление командой и процессами"},
	{keys: []string{"инициатив*"}, hint: "Проактивность и инициативность"},
	{keys: []string{"гибк*", "адапт*"}, hint: "Адаптивность к изменениям"},
	{keys: []string{"системн*"}, hint: "Системное мышление"},
	{keys: []string{"лидер*"}, hint: "Лидерство"},
	{keys: []string{"самообразова*", "развит*"}, hint: "Непрерывное саморазвитие"},
	{keys: []string{"креатив*", "творч*"}, hint: "Креативность в решении задач"},
}

// zunTokens разбивает текст на нормализованные токены: нижний регистр,
// ё->е, разделители — всё, что не буква и не цифра. Токены сохраняют цифры:
// «1с», «Power BI» -> "power","bi".
func zunTokens(text string) []string {
	text = strings.ToLower(strings.ReplaceAll(text, "ё", "е"))
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		if field != "" {
			tokens = append(tokens, field)
		}
	}
	return tokens
}

// zunMatchKey reports whether one lexicon key occurs in tokens.
//
// Key grammar: optional trailing "*" switches token matching from exact equality
// to prefix matching; the space-separated key is a phrase that must be found as
// consecutive tokens.
func zunMatchKey(tokens []string, key string) bool {
	key = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "ё", "е"))
	if key == "" {
		return false
	}
	prefix := strings.HasSuffix(key, "*")
	key = strings.TrimSuffix(key, "*")
	parts := strings.Fields(key)
	if len(parts) == 0 {
		return false
	}
	if len(parts) == 1 {
		for _, token := range tokens {
			if prefix {
				if strings.HasPrefix(token, parts[0]) {
					return true
				}
				continue
			}
			if token == parts[0] {
				return true
			}
		}
		return false
	}
	for start := 0; start+len(parts) <= len(tokens); start++ {
		matched := true
		for offset, part := range parts {
			token := tokens[start+offset]
			if prefix {
				if !strings.HasPrefix(token, part) {
					matched = false
					break
				}
				continue
			}
			if token != part {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// zunRuleMatches reports whether any key of the rule occurs in the text tokens.
func zunRuleMatches(tokens []string, keys []string) bool {
	for _, key := range keys {
		if zunMatchKey(tokens, key) {
			return true
		}
	}
	return false
}

// zunRuleHints собирает подсказки сработавших правил в порядке объявления,
// без дублей и не больше zunSuggestionMaxPerList пунктов. Возвращаемый срез
// всегда не nil, чтобы JSON отдавал [] вместо null.
func zunRuleHints(tokens []string, rules []zunRule) []string {
	hints := make([]string, 0, zunSuggestionMaxPerList)
	seen := make(map[string]struct{}, zunSuggestionMaxPerList)
	for _, rule := range rules {
		if len(hints) >= zunSuggestionMaxPerList {
			break
		}
		if rule.hint == "" || !zunRuleMatches(tokens, rule.keys) {
			continue
		}
		key := strings.ToLower(rule.hint)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		hints = append(hints, rule.hint)
	}
	return hints
}

// zunNodeRuleTokens собирает текст узла для правил. Цитаты ограничиваются по
// длине, чтобы патологически большой источник не раздувал работу хендлера;
// на детерминизм это не влияет.
func zunNodeRuleTokens(profession string, node domain.Node) []string {
	parts := make([]string, 0, 3+len(node.Evidence))
	parts = append(parts, node.Title, node.Description, profession)
	for _, evidence := range node.Evidence {
		parts = append(parts, zunTruncateRunes(evidence.Quote, zunRuleEvidenceRunes))
	}
	return zunTokens(strings.Join(parts, " "))
}

// zunTruncateRunes обрезает строку по рунам, не ломая UTF-8.
func zunTruncateRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if limit <= 0 || len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}

// zunIsEmpty reports whether the node has no meaningful ЗУН values: nil,
// all lists empty or whitespace-only. Such a node can accept a rule suggestion.
func zunIsEmpty(zun *domain.Zun) bool {
	if zun == nil {
		return true
	}
	for _, list := range [][]string{zun.Knowledge, zun.Skills, zun.Abilities} {
		for _, item := range list {
			if strings.TrimSpace(item) != "" {
				return false
			}
		}
	}
	return true
}

// zunFallbackKnowledge — детерминированное знание, когда ни одно правило не
// сработало: предметная область прогона или название узла.
func zunFallbackKnowledge(profession string, node domain.Node) []string {
	if value := zunTruncateRunes(profession, 120); value != "" {
		return []string{"Предметная область: " + value}
	}
	if value := zunTruncateRunes(node.Title, 120); value != "" {
		return []string{"Предметная область: " + value}
	}
	return []string{"Предметная область профессиональной деятельности"}
}

// zunFallbackSkills — детерминированное умение по типу узла дерева.
func zunFallbackSkills(node domain.Node) []string {
	title := zunTruncateRunes(node.Title, 120)
	switch node.Type {
	case domain.NodeRole:
		return []string{"Планировать и организовывать деятельность в рамках роли"}
	case domain.NodeFunction:
		if title != "" {
			return []string{"Выполнять функции: " + title}
		}
		return []string{"Выполнять закреплённые функции"}
	case domain.NodeTask:
		if title != "" {
			return []string{"Решать задачи: " + title}
		}
		return []string{"Решать профессиональные задачи"}
	case domain.NodeAction:
		if title != "" {
			return []string{"Выполнять действия: " + title}
		}
		return []string{"Выполнять трудовые действия"}
	default:
		return []string{"Выполнять трудовые действия по регламенту"}
	}
}

// zunFallbackAbilities — детерминированные abilities по типу узла дерева.
func zunFallbackAbilities(node domain.Node) []string {
	switch node.Type {
	case domain.NodeRole:
		return []string{"Системное мышление"}
	case domain.NodeFunction:
		return []string{"Организованность и последовательность"}
	case domain.NodeTask:
		return []string{"Ответственность за результат"}
	case domain.NodeAction:
		return []string{"Внимательность и аккуратность"}
	default:
		return []string{"Ответственность за результат"}
	}
}

// zunSuggestion — предложение ЗУН для одного узла дерева.
//
// current_zun показывает сохранённый ЗУН узла (null, если его нет). Для узлов с
// непустым ЗУН массив knowledge/skills/abilities пуст, can_apply=false: правила
// не перезаписывают ручные и уже сгенерированные значения. source всегда
// "rule", чтобы UI отличал правило от LLM-enrichment.
type zunSuggestion struct {
	NodeID     string      `json:"node_id"`
	NodeTitle  string      `json:"node_title"`
	Knowledge  []string    `json:"knowledge"`
	Skills     []string    `json:"skills"`
	Abilities  []string    `json:"abilities"`
	Source     string      `json:"source"`
	CurrentZun *domain.Zun `json:"current_zun"`
	CanApply   bool        `json:"can_apply"`
}

// buildZunSuggestion строит детерминированное предложение для одного узла.
// Для узла с непустым ЗУН предложение помечается как неприменимое и не
// содержит новых значений.
func buildZunSuggestion(profession string, node domain.Node) zunSuggestion {
	suggestion := zunSuggestion{
		NodeID:     node.ID,
		NodeTitle:  node.Title,
		Knowledge:  []string{},
		Skills:     []string{},
		Abilities:  []string{},
		Source:     zunSuggestionSourceRule,
		CurrentZun: node.Zun,
	}
	if !zunIsEmpty(node.Zun) {
		return suggestion
	}

	tokens := zunNodeRuleTokens(profession, node)
	knowledge := zunRuleHints(tokens, zunKnowledgeRules)
	skills := zunRuleHints(tokens, zunSkillRules)
	abilities := zunRuleHints(tokens, zunAbilityRules)
	if len(knowledge) == 0 {
		knowledge = zunFallbackKnowledge(profession, node)
	}
	if len(skills) == 0 {
		skills = zunFallbackSkills(node)
	}
	if len(abilities) == 0 {
		abilities = zunFallbackAbilities(node)
	}
	suggestion.Knowledge = knowledge
	suggestion.Skills = skills
	suggestion.Abilities = abilities
	suggestion.CanApply = len(knowledge)+len(skills)+len(abilities) > 0
	return suggestion
}
