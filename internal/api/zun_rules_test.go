package api

import (
	"reflect"
	"strings"
	"testing"

	"activity-descriptor/internal/domain"
)

func TestZunTokens(t *testing.T) {
	tokens := zunTokens("Цифры: 1С, Power BI, ёлка; SQL-запросы!")
	want := []string{"цифры", "1с", "power", "bi", "елка", "sql", "запросы"}
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("токены = %v, ожидали %v", tokens, want)
	}
}

func TestZunMatchKey(t *testing.T) {
	tokens := zunTokens("Пишу SQL-запросы, анализирую отчёты в Power BI и Excel")
	tests := []struct {
		key  string
		want bool
	}{
		{key: "sql", want: true},
		{key: "запрос*", want: true},
		{key: "анализ*", want: true},
		{key: "отчет*", want: true},
		{key: "power bi", want: true},
		{key: "power excel", want: false},
		{key: "python", want: false},
		{key: "запрос", want: false}, // без * — точное совпадение, токен «запросы»
	}
	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			if got := zunMatchKey(tokens, test.key); got != test.want {
				t.Fatalf("zunMatchKey(%q) = %v, ожидали %v", test.key, got, test.want)
			}
		})
	}
}

func TestZunRuleHintsDeterministicAndCapped(t *testing.T) {
	tokens := zunTokens("SQL PostgreSQL Python Excel Docker анализировать отчётность планировать контроль ответственность команда клиенты")
	first := zunRuleHints(tokens, zunKnowledgeRules)
	second := zunRuleHints(tokens, zunKnowledgeRules)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("подсказки не детерминированы: %v vs %v", first, second)
	}
	if len(first) != zunSuggestionMaxPerList {
		t.Fatalf("ожидали ровно %d подсказок, получили %d: %v", zunSuggestionMaxPerList, len(first), first)
	}
	// Порядок правил фиксирован: SQL и PostgreSQL идут раньше Python/Excel/Docker.
	if !strings.Contains(first[0], "SQL") || !strings.Contains(first[1], "PostgreSQL") {
		t.Fatalf("неверный порядок подсказок: %v", first)
	}
}

func TestZunIsEmpty(t *testing.T) {
	tests := []struct {
		name string
		zun  *domain.Zun
		want bool
	}{
		{name: "nil", zun: nil, want: true},
		{name: "zero", zun: &domain.Zun{}, want: true},
		{name: "empty slices", zun: &domain.Zun{Knowledge: []string{}, Skills: []string{}, Abilities: []string{}}, want: true},
		{name: "whitespace", zun: &domain.Zun{Knowledge: []string{"  "}, Skills: []string{""}}, want: true},
		{name: "knowledge", zun: &domain.Zun{Knowledge: []string{"SQL"}}, want: false},
		{name: "skills", zun: &domain.Zun{Skills: []string{"Писать запросы"}}, want: false},
		{name: "abilities", zun: &domain.Zun{Abilities: []string{"Ответственность"}}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := zunIsEmpty(test.zun); got != test.want {
				t.Fatalf("zunIsEmpty(%+v) = %v, ожидали %v", test.zun, got, test.want)
			}
		})
	}
}

func TestBuildZunSuggestionRules(t *testing.T) {
	node := domain.Node{
		ID:          "node-1",
		Type:        domain.NodeTask,
		Title:       "Готовить SQL-отчёты",
		Description: "Строить дашборды в Power BI",
		Evidence: []domain.Evidence{
			{ID: "ev-1", RefID: "answer-1", Quote: "Каждый день пишу SQL-запросы и обновляю дашборд в Power BI"},
		},
	}
	suggestion := buildZunSuggestion("Аналитик данных", node)
	if suggestion.NodeID != "node-1" || suggestion.NodeTitle != "Готовить SQL-отчёты" {
		t.Fatalf("идентификация узла потеряна: %+v", suggestion)
	}
	if suggestion.Source != zunSuggestionSourceRule || !suggestion.CanApply || suggestion.CurrentZun != nil {
		t.Fatalf("ожидали применимое rule-предложение без current_zun: %+v", suggestion)
	}
	for name, list := range map[string][]string{
		"knowledge": suggestion.Knowledge,
		"skills":    suggestion.Skills,
		"abilities": suggestion.Abilities,
	} {
		if len(list) == 0 {
			t.Fatalf("%s пуст", name)
		}
		if len(list) > zunSuggestionMaxPerList {
			t.Fatalf("%s больше %d пунктов: %v", name, zunSuggestionMaxPerList, list)
		}
	}
	knowledge := strings.Join(suggestion.Knowledge, "|")
	for _, want := range []string{"SQL-запросы и реляционные базы данных", "Power BI"} {
		if !strings.Contains(knowledge, want) {
			t.Fatalf("knowledge не содержит %q: %v", want, suggestion.Knowledge)
		}
	}
	skills := strings.Join(suggestion.Skills, "|")
	if !strings.Contains(skills, "Писать SQL-запросы") || !strings.Contains(skills, "Готовить отчётность") {
		t.Fatalf("skills не содержат ожидаемых пунктов: %v", suggestion.Skills)
	}
	abilities := strings.Join(suggestion.Abilities, "|")
	if abilities == "" {
		t.Fatalf("abilities пуст: %v", suggestion.Abilities)
	}

	// Детерминизм: повторный вызов даёт ровно то же самое.
	again := buildZunSuggestion("Аналитик данных", node)
	if !reflect.DeepEqual(suggestion, again) {
		t.Fatalf("предложение не детерминировано: %+v vs %+v", suggestion, again)
	}
}

func TestBuildZunSuggestionFallbacks(t *testing.T) {
	tests := []struct {
		name          string
		profession    string
		node          domain.Node
		wantKnowledge string
		wantSkill     string
		wantAbility   string
	}{
		{
			name:          "role without matches",
			profession:    "",
			node:          domain.Node{ID: "n", Type: domain.NodeRole, Title: "Повар"},
			wantKnowledge: "Предметная область: Повар",
			wantSkill:     "Планировать и организовывать деятельность в рамках роли",
			wantAbility:   "Системное мышление",
		},
		{
			name:          "function with profession",
			profession:    "Склад",
			node:          domain.Node{ID: "n", Type: domain.NodeFunction, Title: "Вести учёт"},
			wantKnowledge: "Предметная область: Склад",
			wantSkill:     "Выполнять функции: Вести учёт",
			wantAbility:   "Организованность и последовательность",
		},
		{
			name:          "task",
			profession:    "Логист",
			node:          domain.Node{ID: "n", Type: domain.NodeTask, Title: "Маршрут доставки"},
			wantKnowledge: "Предметная область: Логист",
			wantSkill:     "Решать задачи: Маршрут доставки",
			wantAbility:   "Ответственность за результат",
		},
		{
			name:          "action",
			profession:    "Оператор",
			node:          domain.Node{ID: "n", Type: domain.NodeAction, Title: "Запустить станок"},
			wantKnowledge: "Предметная область: Оператор",
			wantSkill:     "Выполнять действия: Запустить станок",
			wantAbility:   "Внимательность и аккуратность",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			suggestion := buildZunSuggestion(test.profession, test.node)
			if len(suggestion.Knowledge) != 1 || suggestion.Knowledge[0] != test.wantKnowledge {
				t.Fatalf("knowledge = %v, ожидали [%q]", suggestion.Knowledge, test.wantKnowledge)
			}
			if len(suggestion.Skills) != 1 || suggestion.Skills[0] != test.wantSkill {
				t.Fatalf("skills = %v, ожидали [%q]", suggestion.Skills, test.wantSkill)
			}
			if len(suggestion.Abilities) != 1 || suggestion.Abilities[0] != test.wantAbility {
				t.Fatalf("abilities = %v, ожидали [%q]", suggestion.Abilities, test.wantAbility)
			}
			if !suggestion.CanApply {
				t.Fatal("fallback-предложение должно быть применимо")
			}
		})
	}
}

func TestBuildZunSuggestionProtectsExistingZun(t *testing.T) {
	current := &domain.Zun{
		Knowledge: []string{"Ручное знание"},
		Skills:    []string{"Ручное умение"},
		Abilities: []string{"Ручная способность"},
	}
	node := domain.Node{ID: "node-set", Type: domain.NodeTask, Title: "Готовить SQL-отчёты", Zun: current}
	suggestion := buildZunSuggestion("Аналитик данных", node)
	if suggestion.CanApply {
		t.Fatal("узел с непустым ЗУН не должен быть применим")
	}
	if len(suggestion.Knowledge) != 0 || len(suggestion.Skills) != 0 || len(suggestion.Abilities) != 0 {
		t.Fatalf("для узла с ЗУН предложение должно быть пустым: %+v", suggestion)
	}
	if suggestion.CurrentZun == nil || suggestion.CurrentZun.Knowledge[0] != "Ручное знание" {
		t.Fatalf("текущий ЗУН потерян: %+v", suggestion.CurrentZun)
	}

	// Пустой, но не nil ЗУН не блокирует предложение.
	empty := &domain.Zun{Knowledge: []string{}, Skills: []string{}, Abilities: []string{}}
	emptyNode := domain.Node{ID: "node-empty", Type: domain.NodeTask, Title: "Готовить SQL-отчёты", Zun: empty}
	emptySuggestion := buildZunSuggestion("Аналитик данных", emptyNode)
	if !emptySuggestion.CanApply || len(emptySuggestion.Knowledge) == 0 {
		t.Fatalf("пустой ЗУН должен получать предложение: %+v", emptySuggestion)
	}
}

// TestBuildZunSuggestionTenNodes — требование фичи: правила проверены на
// дереве из 10 узлов, ответ детерминирован и не перезаписывает ЗУН.
func TestBuildZunSuggestionTenNodes(t *testing.T) {
	nodes := []domain.Node{
		{ID: "n1", Type: domain.NodeRole, Title: "Аналитик данных", Description: "Анализирует показатели"},
		{ID: "n2", Type: domain.NodeFunction, Title: "Отчётность", Description: "Готовит отчёты в Excel"},
		{ID: "n3", Type: domain.NodeTask, Title: "SQL-выгрузки", Evidence: []domain.Evidence{{Quote: "пишу SQL-запросы"}}},
		{ID: "n4", Type: domain.NodeAction, Title: "Проверка данных", Description: "Контролирует качество"},
		{ID: "n5", Type: domain.NodeAction, Title: "Дашборды", Description: "Строит дашборды в Power BI"},
		{ID: "n6", Type: domain.NodeTask, Title: "Переговоры", Description: "Ведёт переговоры с заказчиком", Evidence: []domain.Evidence{{Quote: "согласовываю требования"}}},
		{ID: "n7", Type: domain.NodeFunction, Title: "Управление командой", Description: "Руководит командой, контролирует сроки"},
		{ID: "n8", Type: domain.NodeAction, Title: "Документация", Description: "Оформляет документацию по ГОСТ"},
		{ID: "n9", Type: domain.NodeTask, Title: "Бюджеты", Description: "Планирует бюджеты и контролирует расходы"},
		{ID: "n10", Type: domain.NodeRole, Title: "Наставничество", Description: "Обучает новых сотрудников", Zun: &domain.Zun{Knowledge: []string{"Ручное значение"}}},
	}
	first := make([]zunSuggestion, 0, len(nodes))
	for _, node := range nodes {
		first = append(first, buildZunSuggestion("Аналитик данных", node))
	}
	second := make([]zunSuggestion, 0, len(nodes))
	for _, node := range nodes {
		second = append(second, buildZunSuggestion("Аналитик данных", node))
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("предложения на 10 узлах не детерминированы")
	}
	if len(first) != 10 {
		t.Fatalf("ожидали 10 предложений, получили %d", len(first))
	}
	for index, suggestion := range first {
		if suggestion.NodeID != nodes[index].ID {
			t.Fatalf("порядок узлов нарушен: %q vs %q", suggestion.NodeID, nodes[index].ID)
		}
		if index == len(nodes)-1 {
			if suggestion.CanApply || len(suggestion.Knowledge) != 0 {
				t.Fatalf("узел с ручным ЗУН не защищён: %+v", suggestion)
			}
			continue
		}
		if !suggestion.CanApply {
			t.Fatalf("узел %s должен получать предложение", suggestion.NodeID)
		}
		if len(suggestion.Knowledge)+len(suggestion.Skills)+len(suggestion.Abilities) == 0 {
			t.Fatalf("узел %s получил пустое предложение", suggestion.NodeID)
		}
	}
}
