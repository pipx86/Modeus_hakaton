package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

const zunBatchSize = 4

type zunNodeRef struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

type zunByNode map[string]domain.Zun

// enrichZun просит модель проставить знания, умения и навыки для узлов дерева.
//
// Узлы отправляются небольшими батчами: один большой ответ модель иногда не
// успевает вернуть до таймаута провайдера. Ошибка любого батча не ломает
// прогон — дерево сохраняется без ЗУН для этих узлов.
func enrichZun(ctx context.Context, client llm.ChatClient, profession string, nodes []domain.Node) []domain.Node {
	if client == nil || len(nodes) == 0 {
		return nodes
	}

	for start := 0; start < len(nodes); start += zunBatchSize {
		end := start + zunBatchSize
		if end > len(nodes) {
			end = len(nodes)
		}
		batch := nodes[start:end]
		parsed, err := requestZunBatch(ctx, client, profession, batch)
		if err != nil {
			log.Printf("chat: zun batch %d-%d skipped: %v", start, end-1, err)
			continue
		}
		for index := range batch {
			zun, ok := parsed[batch[index].ID]
			if !ok {
				continue
			}
			copied := zun
			nodes[start+index].Zun = &copied
		}
	}
	return nodes
}

func requestZunBatch(ctx context.Context, client llm.ChatClient, profession string, nodes []domain.Node) (zunByNode, error) {
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
		return nil, err
	}

	prompt := fmt.Sprintf(`Профессия: %s.

Для каждого узла дерева деятельности перечисли знания, умения и навыки (ЗУН),
которые нужны для его выполнения. Пиши кратко: 2–4 пункта в каждом списке.

Верни строго JSON-объект без пояснений и markdown: ключ — id узла,
значение — объект с массивами строк:
{"<id>": {"knowledge": ["..."], "skills": ["..."], "abilities": ["..."]}}

Узлы: %s

Пиши только на русском языке.`, profession, string(payload))

	answer, err := client.Chat(ctx, []llm.Message{{Role: llm.RoleUser, Content: prompt}})
	if err != nil {
		return nil, err
	}

	var parsed zunByNode
	if err := json.Unmarshal([]byte(trimCodeFence(answer)), &parsed); err != nil {
		return nil, fmt.Errorf("decode zun: %w", err)
	}
	return parsed, nil
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
