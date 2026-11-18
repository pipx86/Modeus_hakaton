package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

// The scripted client tells the agents apart by the output schema each agent
// asks for in its user prompt. The system prompt is shared by all agents (it
// starts with the common security rules), so a substring there is not a reliable
// discriminator; the schema name is part of the agent contract and is stable.
const (
	interviewerSchemaMarker = "interviewer.output.schema.json"
	analystSchemaMarker     = "analyst.output.schema.json"
)

var (
	// nextLevelRe extracts "next_level" from the interviewer input JSON.
	nextLevelRe = regexp.MustCompile(`"next_level"\s*:\s*"([a-z]+)"`)
	// needClarificationRe detects a clarification turn in the same JSON.
	needClarificationRe = regexp.MustCompile(`"need_clarification"\s*:\s*true`)
)

// defaultQuestionText is the deterministic fake interviewer question per level.
var defaultQuestionText = map[domain.QuestionLevel]string{
	domain.QuestionRole:     "Опишите, чем вы занимаетесь в этой роли и за что отвечаете.",
	domain.QuestionFunction: "Назовите крупные направления работы, которые входят в вашу роль.",
	domain.QuestionTask:     "Какие конкретные задачи вы выполняете регулярно?",
	domain.QuestionAction:   "Какие конкретные действия и инструменты вы используете в работе?",
}

// scriptClient is an in-process llm.ChatClient that replays one golden case.
// It never performs I/O and produces byte-identical answers for identical
// inputs, so evaluation runs are reproducible.
type scriptClient struct {
	script           Script
	interviewerCalls int
	analystCalls     int
}

// newScriptClient returns a fake client for one case.
func newScriptClient(script Script) *scriptClient {
	return &scriptClient{script: script}
}

// Chat implements llm.ChatClient.
func (c *scriptClient) Chat(_ context.Context, messages []llm.Message) (string, error) {
	if len(messages) == 0 {
		return "", errors.New("eval: scripted client: empty messages")
	}
	agentKind, err := detectAgent(messages)
	if err != nil {
		return "", err
	}
	lastUser := messages[len(messages)-1].Content
	switch agentKind {
	case analystKind:
		c.analystCalls++
		data, err := json.Marshal(c.script.Analyst)
		if err != nil {
			return "", fmt.Errorf("eval: scripted analyst: %w", err)
		}
		return string(data), nil
	default:
		c.interviewerCalls++
		if c.mustFail(c.interviewerCalls) {
			return "", fmt.Errorf("eval: scripted interviewer failure on call %d", c.interviewerCalls)
		}
		return c.interviewerAnswer(lastUser), nil
	}
}

const (
	interviewerKind = "interviewer"
	analystKind     = "analyst"
)

func detectAgent(messages []llm.Message) (string, error) {
	for _, message := range messages {
		switch {
		case strings.Contains(message.Content, analystSchemaMarker):
			return analystKind, nil
		case strings.Contains(message.Content, interviewerSchemaMarker):
			return interviewerKind, nil
		}
	}
	return "", errors.New("eval: scripted client: unrecognized agent prompt")
}

func (c *scriptClient) mustFail(interviewerCall int) bool {
	for _, call := range c.script.Interviewer.ErrorOnCalls {
		if call == interviewerCall {
			return true
		}
	}
	return false
}

func (c *scriptClient) interviewerAnswer(userPrompt string) string {
	level := domain.QuestionRole
	if match := nextLevelRe.FindStringSubmatch(userPrompt); len(match) == 2 {
		if candidate := domain.QuestionLevel(match[1]); candidate.Valid() {
			level = candidate
		}
	}
	text := defaultQuestionText[level]
	if override := strings.TrimSpace(c.script.Interviewer.Questions[string(level)]); override != "" {
		text = override
	}
	out := agent.InterviewerOutput{
		Question: domain.Question{
			ID:    "q",
			Level: level,
			Text:  text,
		},
		Rationale:       "Scripted eval question for level " + string(level) + ".",
		IsClarification: needClarificationRe.MatchString(userPrompt),
	}
	data, err := json.Marshal(out)
	if err != nil {
		// The struct above is fixed and always marshalable.
		return `{"question":{"id":"q","level":"role","text":"fallback"},"rationale":"marshal error","is_clarification":false}`
	}
	return string(data)
}
