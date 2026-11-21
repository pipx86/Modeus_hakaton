package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// scenarioJSON — встроенный сценарий интервью. Сценарий не зависит от сети и
// от модели: он воспроизводит один и тот же диалог на каждом прогоне, поэтому
// подходит для проверки API и пайплайна после падения провайдера.
//
//go:embed scenario.json
var scenarioJSON []byte

// scenarioTurn — одна пара «вопрос интервьюера → ответ респондента».
type scenarioTurn struct {
	// Level — ожидаемый уровень дерева: role, function, task или action.
	// CLI его не отправляет, но dry-run показывает, какой материал даёт ответ.
	Level    string `json:"level"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// scenario — встроенный сценарий целиком.
type scenario struct {
	Version    int            `json:"version"`
	Profession string         `json:"profession"`
	Turns      []scenarioTurn `json:"turns"`
}

// loadScenario разбирает встроенный JSON. Ошибка означает повреждённый бинарь,
// а не проблему окружения: вызывающий код завершает прогон с кодом 2.
func loadScenario() (scenario, error) {
	var parsed scenario
	if err := json.Unmarshal(scenarioJSON, &parsed); err != nil {
		return scenario{}, fmt.Errorf("встроенный сценарий повреждён: %w", err)
	}
	if err := parsed.validate(); err != nil {
		return scenario{}, fmt.Errorf("встроенный сценарий невалиден: %w", err)
	}
	return parsed, nil
}

// validate проверяет инварианты, на которые опираются тесты и документация:
// все четыре уровня дерева, минимум пять ответов и отсутствие пустых строк.
func (s scenario) validate() error {
	if strings.TrimSpace(s.Profession) == "" {
		return fmt.Errorf("не задана профессия по умолчанию")
	}
	if len(s.Turns) < 5 {
		return fmt.Errorf("нужно минимум 5 вопросов, есть %d", len(s.Turns))
	}
	levels := make(map[string]bool, 4)
	for index, turn := range s.Turns {
		level := strings.TrimSpace(turn.Level)
		switch level {
		case "role", "function", "task", "action":
			levels[level] = true
		default:
			return fmt.Errorf("вопрос %d: неизвестный уровень %q", index+1, turn.Level)
		}
		if strings.TrimSpace(turn.Question) == "" {
			return fmt.Errorf("вопрос %d пустой", index+1)
		}
		if strings.TrimSpace(turn.Answer) == "" {
			return fmt.Errorf("ответ %d пустой", index+1)
		}
	}
	for _, level := range []string{"role", "function", "task", "action"} {
		if !levels[level] {
			return fmt.Errorf("в сценарии нет уровня %q", level)
		}
	}
	return nil
}

// answers возвращает ответы респондента в порядке сценария — ровно то, что
// CLI отправляет через POST /runs/{id}/messages.
func (s scenario) answers() []string {
	result := make([]string, 0, len(s.Turns))
	for _, turn := range s.Turns {
		result = append(result, turn.Answer)
	}
	return result
}
