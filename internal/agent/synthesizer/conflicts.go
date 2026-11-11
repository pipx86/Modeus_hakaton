package synthesizer

import (
	"encoding/json"
	"fmt"
	"strings"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
)

// DetectConflicts finds nodes that share a canonical id but disagree on title or
// description across sources. Values are ordered by source priority, and the
// winner is the highest-priority value (interview > standard > vacancy).
func DetectConflicts(groups map[string][]sourcedNode, ids []string) []domain.Conflict {
	conflicts := make([]domain.Conflict, 0)
	for _, id := range ids {
		items := groups[id]
		if len(items) < 2 {
			continue
		}
		for _, field := range []string{"title", "description"} {
			values := make([]domain.ConflictValue, 0, len(items))
			seen := make(map[string]bool, len(items))
			for _, item := range items {
				value := fieldValue(item.node, field)
				if strings.TrimSpace(value) == "" {
					continue
				}
				key := string(item.source.Type) + "\x00" + value
				if seen[key] {
					continue
				}
				seen[key] = true
				values = append(values, domain.ConflictValue{
					SourceID:   item.source.ID,
					SourceType: item.source.Type,
					Value:      value,
					Priority:   item.source.EffectivePriority(),
				})
			}
			if len(values) < 2 || !hasDistinctValues(values) {
				continue
			}
			winner, reason := domain.ResolveConflict(values)
			conflicts = append(conflicts, domain.Conflict{
				ID:     fmt.Sprintf("c-%d", len(conflicts)+1),
				NodeID: id,
				Field:  field,
				Values: values,
				Winner: winner,
				Reason: reason,
			})
		}
	}
	return conflicts
}

// ConflictMessages turns resolved conflicts into agent memory messages. Each
// message payload is the serialized domain.Conflict.
func ConflictMessages(conflicts []domain.Conflict) []agent.AgentMessage {
	messages := make([]agent.AgentMessage, 0, len(conflicts))
	for i, conflict := range conflicts {
		payload, err := json.Marshal(conflict)
		if err != nil {
			payload = nil
		}
		messages = append(messages, agent.AgentMessage{
			ID:   fmt.Sprintf("msg-conflict-%d", i+1),
			Kind: "conflict",
			Text: fmt.Sprintf("Конфликт %s узла %s: побеждает %s (%s)",
				conflict.Field, conflict.NodeID, conflict.Winner.SourceType, conflict.Winner.Value),
			Payload: payload,
		})
	}
	return messages
}

func fieldValue(node domain.Node, field string) string {
	switch field {
	case "title":
		return node.Title
	case "description":
		return node.Description
	default:
		return ""
	}
}

func hasDistinctValues(values []domain.ConflictValue) bool {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		seen[value.Value] = true
	}
	return len(seen) > 1
}
