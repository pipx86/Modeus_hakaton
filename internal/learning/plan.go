package learning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

// ErrUnavailable means the plan generator has no LLM client configured.
var ErrUnavailable = errors.New("learning plan generator is unavailable")

// Plan is an actionable learning roadmap for a beginner.
type Plan struct {
	Profession string   `json:"profession"`
	Summary    string   `json:"summary"`
	Weeks      int      `json:"weeks"`
	Stages     []Stage  `json:"stages"`
	Sources    []string `json:"sources,omitempty"`
}

// Stage is one step of the plan.
type Stage struct {
	Title    string   `json:"title"`
	Goal     string   `json:"goal"`
	Topics   []string `json:"topics"`
	Practice []string `json:"practice"`
}

// Generate builds a plan from real vacancy/material snippets. The model must
// answer with strict JSON; malformed answers are rejected instead of being
// rendered as a half-broken plan.
func Generate(ctx context.Context, client llm.ChatClient, profession string, sources []domain.Source) (Plan, error) {
	return GenerateWithBackground(ctx, client, profession, sources, "")
}

// GenerateWithBackground also uses the user's interview answers, so the plan
// starts from what the person already knows instead of repeating the basics.
func GenerateWithBackground(ctx context.Context, client llm.ChatClient, profession string, sources []domain.Source, background string) (Plan, error) {
	if client == nil {
		return Plan{}, ErrUnavailable
	}
	profession = strings.TrimSpace(profession)
	if profession == "" {
		return Plan{}, errors.New("profession is empty")
	}
	prompt := buildPrompt(profession, sources, background)
	answer, err := client.Chat(ctx, []llm.Message{{Role: llm.RoleUser, Content: prompt}})
	if err != nil {
		return Plan{}, fmt.Errorf("learning plan llm: %w", err)
	}
	var plan Plan
	if err := json.Unmarshal([]byte(trimFence(answer)), &plan); err != nil {
		return Plan{}, fmt.Errorf("decode learning plan: %w", err)
	}
	plan.Profession = profession
	normalize(&plan)
	if len(plan.Stages) == 0 {
		return Plan{}, errors.New("learning plan has no stages")
	}
	return plan, nil
}

func buildPrompt(profession string, sources []domain.Source, background string) string {
	var contextBuilder strings.Builder
	for index, source := range sources {
		if index >= 8 {
			break
		}
		content := strings.TrimSpace(source.Content)
		if content == "" {
			continue
		}
		if len([]rune(content)) > 1200 {
			content = string([]rune(content)[:1200]) + "…"
		}
		fmt.Fprintf(&contextBuilder, "\n[%d] %s\n%s\n", index+1, source.Title, content)
	}
	if contextBuilder.Len() == 0 {
		contextBuilder.WriteString("Источники не переданы. Опирайся на актуальные требования рынка и здравый смысл.")
	}
	background = strings.TrimSpace(background)
	if len([]rune(background)) > 5000 {
		background = string([]rune(background)[:5000]) + "…"
	}
	if background == "" {
		background = "Пользователь не сообщил предыдущий опыт. Считай, что он начинает с нуля."
	}
	return fmt.Sprintf(`Ты карьерный методист. Составь актуальный план обучения профессии «%s» для новичка.

Что пользователь уже умеет и какой опыт указал в интервью:
%s

Требования:
- опирайся на требования из вакансий и материалов ниже;
- не повторяй темы, которые пользователь уже уверенно знает; начни с ближайшего пробела;
- если опыта мало, начни с базы; если опыта больше, сократи вводные этапы;
- план должен быть практическим: что изучить, что сделать руками и в каком порядке;
- 4-6 этапов, в каждом 3-5 тем и 2-4 практических задания;
- без воды, без мотивационных лозунгов, без выдуманных ссылок;
- пиши только на русском языке;
- верни строго JSON без markdown и пояснений:
{"summary":"краткое описание пути","weeks":12,"stages":[{"title":"этап","goal":"цель этапа","topics":["тема"],"practice":["задание"]}]}

Источники:%s`, profession, background, contextBuilder.String())
}

func normalize(plan *Plan) {
	plan.Summary = strings.TrimSpace(plan.Summary)
	if plan.Weeks < 1 || plan.Weeks > 104 {
		plan.Weeks = 12
	}
	if len(plan.Stages) > 8 {
		plan.Stages = plan.Stages[:8]
	}
	for index := range plan.Stages {
		stage := &plan.Stages[index]
		stage.Title = strings.TrimSpace(stage.Title)
		stage.Goal = strings.TrimSpace(stage.Goal)
		stage.Topics = cleanList(stage.Topics)
		stage.Practice = cleanList(stage.Practice)
	}
}

func cleanList(values []string) []string {
	clean := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		clean = append(clean, value)
	}
	return clean
}

func trimFence(answer string) string {
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
