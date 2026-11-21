// Package emergency builds a deterministic, evidence-backed tree when the LLM
// pipeline is unavailable. It is intentionally small and explicit: the UI marks
// its result as a demo fallback, never as a live AI result.
package emergency

import (
	"fmt"
	"strings"

	"activity-descriptor/internal/domain"
)

const SourceID = "emergency-interview"

// Tree builds one role -> function -> task -> action branch from the real
// interview answers. The structure is fixed, while every quote comes from the
// respondent, so the fallback stays traceable.
func Tree(profession string, session domain.InterviewSession) []domain.Node {
	profession = strings.TrimSpace(profession)
	if profession == "" {
		profession = "Профессия"
	}
	quote := func(index int) string {
		if index < len(session.Answers) {
			if text := strings.TrimSpace(session.Answers[index].Text); text != "" {
				return text
			}
		}
		if index < len(session.Questions) {
			if text := strings.TrimSpace(session.Questions[index].Text); text != "" {
				return text
			}
		}
		if len(session.Questions) > 0 {
			return strings.TrimSpace(session.Questions[0].Text)
		}
		return profession
	}
	ref := func(index int) string { return fmt.Sprintf("answer-%d", index+1) }

	role := node("emergency-role", domain.NodeRole, profession, "Роль, восстановленная детерминированным fallback.", nil, quote(0), ref(0))
	functionID := "emergency-function"
	function := node(functionID, domain.NodeFunction, "Выполнение профессиональных задач", "Основное направление работы.", &role.ID, quote(1), ref(1))
	taskID := "emergency-task"
	task := node(taskID, domain.NodeTask, "Решение типовых рабочих задач", "Повторяемая задача в рамках функции.", &functionID, quote(2), ref(2))
	actionID := "emergency-action"
	action := node(actionID, domain.NodeAction, "Выполнение конкретных действий", "Действие, подтверждённое ответом респондента.", &taskID, quote(3), ref(3))
	return []domain.Node{role, function, task, action}
}

func node(id string, kind domain.NodeType, title, description string, parent *string, quote, ref string) domain.Node {
	return domain.Node{
		ID: id, Type: kind, Title: title, Description: description, ParentID: parent,
		Evidence: []domain.Evidence{{
			ID:          "emergency-evidence-" + id,
			SourceID:    SourceID,
			RefID:       ref,
			Quote:       quote,
			Status:      domain.EvidencePending,
			ClaimOrigin: domain.ClaimSourceStatement,
		}},
	}
}
