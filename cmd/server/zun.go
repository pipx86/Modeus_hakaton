package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

type zunNodeRef struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

type zunByNode map[string]domain.Zun

// enrichZun просит модель проставить знания, умения и навыки для узлов дерева.
//
// Это best-effort шаг: при любой ошибке модели или парсинга дерево сохраняется
// без ЗУН, прогон не падает.
func enrichZun(ctx context.Context, client llm.ChatClient, profession string, nodes []domain.Node) []domain.Node {
	if client == nil || len(nodes) == 0 {
		return nodes
	}

	refs := make([]zunNodeRef, 0, len(nodes))
	for _, node := range nodes {
		refs = append(refs, zunNodeRef{
			ID:          node.ID,
			Type:        string(node.Type),
			Title:       node.Title,
			Description: node.Description,
		})
	}
	payload, err := json.Marshal(refs)
	if err != nil {
		return nodes
	}

	prompt := fmt.Sprintf(`Профессия: %s.

Для каждого узла дерева деятельности перечисли знания, умения и навыки (ЗУН),
которые нужны для его выполнения.

Верни строго JSON-объект без пояснений и markdown: ключ — id узла,
значение — объект с массивами строк:
{"<id>": {"knowledge": ["..."], "skills": ["..."], "abilities": ["..."]}}

Узлы: %s`, profession, string(payload))

	answer, err := client.Chat(ctx, []llm.Message{{Role: llm.RoleUser, Content: prompt}})
	if err != nil {
		return nodes
	}

	var parsed zunByNode
	if err := json.Unmarshal([]byte(trimCodeFence(answer)), &parsed); err != nil {
		return nodes
	}

	for index := range nodes {
		zun, ok := parsed[nodes[index].ID]
		if !ok {
			continue
		}
		copied := zun
		nodes[index].Zun = &copied
	}
	return nodes
}

// trimCodeFence убирает обёртку ```json ... ``` вокруг ответа модели.
func trimCodeFence(answer string) string {
	value := strings.TrimSpace(answer)
	if !strings.HasPrefix(value, "```") {
		return value
	}
	value = strings.TrimPrefix(value, "```json")
	value = strings.TrimPrefix(value, "```")
	if index := strings.LastIndex(value, "```"); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSpace(value)
}
