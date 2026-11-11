// Command e2e runs the interview → analysis → synthesis pipeline end-to-end
// with a deterministic fake model, then prints and optionally saves the tree.
//
// It is the offline e2e v0: no network, no database and no API required. The
// live HTTP e2e will reuse the same pipeline once the API handlers are wired.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llm/agents"
	"activity-descriptor/internal/pipeline"
)

var nextLevelRe = regexp.MustCompile(`"next_level"\s*:\s*"([a-z]+)"`)

// fakeClient implements llm.ChatClient with scripted, schema-valid answers.
// It never touches the network.
type fakeClient struct {
	calls int
}

func (f *fakeClient) Chat(_ context.Context, messages []llm.Message) (string, error) {
	f.calls++
	if len(messages) == 0 {
		return "", errors.New("fake: empty messages")
	}
	system := strings.ToLower(messages[0].Content)
	switch {
	case strings.Contains(system, "ты — аналитик"):
		return analystJSON, nil
	case strings.Contains(system, "ты — интервьюер"):
		level := "role"
		if m := nextLevelRe.FindStringSubmatch(messages[len(messages)-1].Content); len(m) == 2 {
			level = m[1]
		}
		return fmt.Sprintf(`{"question":{"id":"q","level":%q,"text":"Расскажите о конкретной задаче на уровне %s."},"rationale":"План интервью.","is_clarification":false}`, level, level), nil
	default:
		return "", errors.New("fake: unknown agent prompt")
	}
}

const analystJSON = `{
  "entities": [
    {
      "id": "role-1",
      "type": "role",
      "title": "Аналитик данных",
      "description": "Отвечает за отчётность и качество данных",
      "confidence": 0.95,
      "evidence": [{
        "id": "ev-role-1",
        "source_id": "src-interview",
        "ref_id": "answer-1",
        "quote": "Я аналитик данных, отвечаю за отчётность и качество данных в продуктовой команде."
      }]
    },
    {
      "id": "func-1",
      "type": "function",
      "title": "Сбор требований",
      "confidence": 0.9,
      "evidence": [{
        "id": "ev-func-1",
        "source_id": "src-interview",
        "ref_id": "answer-2",
        "quote": "Моя функция — сбор требований, построение дашбордов и проверка качества данных."
      }]
    },
    {
      "id": "task-1",
      "type": "task",
      "title": "Подготовка отчёта по воронке",
      "description": "Еженедельный отчёт по воронке продаж",
      "confidence": 0.9,
      "evidence": [{
        "id": "ev-task-1",
        "source_id": "src-interview",
        "ref_id": "answer-3",
        "quote": "Регулярно готовлю отчёт по воронке продаж и проверяю расхождения в данных."
      }]
    },
    {
      "id": "action-1",
      "type": "action",
      "title": "SQL-выгрузка данных",
      "confidence": 0.85,
      "evidence": [{
        "id": "ev-action-1",
        "source_id": "src-interview",
        "ref_id": "answer-4",
        "quote": "Выгружаю данные SQL-запросами, строю дашборд в BI и сверяю метрики с источником."
      }]
    }
  ]
}`

var answers = map[domain.QuestionLevel]string{
	domain.QuestionRole:     "Я аналитик данных, отвечаю за отчётность и качество данных в продуктовой команде.",
	domain.QuestionFunction: "Моя функция — сбор требований, построение дашбордов и проверка качества данных.",
	domain.QuestionTask:     "Регулярно готовлю отчёт по воронке продаж и проверяю расхождения в данных.",
	domain.QuestionAction:   "Выгружаю данные SQL-запросами, строю дашборд в BI и сверяю метрики с источником.",
}

func answer(q domain.Question) string {
	if text, ok := answers[q.Level]; ok {
		return text
	}
	return "Раз в неделю собираю требования у маркетинга и обновляю словарь метрик."
}

func main() {
	outPath := flag.String("out", "", "optional path for the result JSON")
	live := flag.Bool("live", false, "use the configured Nemotron/OpenRouter provider instead of the fake model")
	flag.Parse()

	fake := &fakeClient{}
	var client, interviewerClient, analystClient llm.ChatClient
	if *live {
		cfg, err := agents.FromEnv()
		if err != nil {
			fmt.Fprintf(os.Stderr, "live config: %v\n", err)
			os.Exit(1)
		}
		pool, err := agents.NewPool(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "live pool: %v\n", err)
			os.Exit(1)
		}
		interviewerClient, _ = pool.Client(agents.Interviewer)
		analystClient, _ = pool.Client(agents.Analyst)
		client = interviewerClient
		fmt.Printf("provider: %s model: %s\n", cfg.BaseURL, cfg.Agents[agents.Interviewer].Model)
	} else {
		guarded, err := llm.NewGuardedClient(fake, llm.WithGuardPolicy(llm.GuardPolicyWrap))
		if err != nil {
			fmt.Fprintf(os.Stderr, "guardrails: %v\n", err)
			os.Exit(1)
		}
		client = guarded
	}

	result, err := pipeline.Run(context.Background(), pipeline.Config{
		Client:            client,
		InterviewerClient: interviewerClient,
		AnalystClient:     analystClient,
		Respondent: domain.Respondent{
			ID:         "resp-1",
			Name:       "Демо-респондент",
			Role:       "Аналитик данных",
			Level:      "middle",
			Profession: "Аналитик данных",
		},
		Profession: "Аналитик данных",
		Answers:    answer,
		Sources: []domain.Source{
			{ID: "src-interview", Type: domain.SourceInterview, Title: "Демо-интервью"},
			{ID: "src-standard", Type: domain.SourceStandard, Title: "Профстандарт аналитика"},
			{ID: "src-vacancy", Type: domain.SourceVacancy, Title: "Вакансия аналитика"},
		},
		Standard: []domain.Node{{
			ID:    "func-1",
			Type:  domain.NodeFunction,
			Title: "Аналитика требований",
			Evidence: []domain.Evidence{{
				ID: "ev-std-1", SourceID: "src-standard", RefID: "std-3.1",
				Quote: "Анализ и формализация требований",
			}},
		}},
		Vacancy: []domain.Node{{
			ID:    "func-1",
			Type:  domain.NodeFunction,
			Title: "Сбор данных",
			Evidence: []domain.Evidence{{
				ID: "ev-vac-1", SourceID: "src-vacancy", RefID: "vac-req-2",
				Quote: "Сбор и подготовка данных",
			}},
		}},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e failed: %v\n", err)
		os.Exit(1)
	}

	payload := map[string]any{
		"run":         result.Run,
		"session":     result.Session,
		"transcript":  result.Transcript,
		"analyst":     result.Analyst,
		"tree":        result.Synthesis.Tree,
		"conflicts":   result.Synthesis.Conflicts,
		"messages":    result.Synthesis.Messages,
		"duration_ms": result.Duration.Milliseconds(),
		"model_calls": fake.calls,
		"live":        *live,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal result: %v\n", err)
		os.Exit(1)
	}
	if *outPath != "" {
		if err := os.WriteFile(*outPath, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", *outPath, err)
			os.Exit(1)
		}
	}

	fmt.Printf("status:       %s\n", result.Run.Status)
	fmt.Printf("duration:     %s\n", result.Duration.Round(time.Millisecond))
	fmt.Printf("questions:    %d\n", len(result.Session.Questions))
	fmt.Printf("entities:     %d\n", len(result.Analyst.Entities))
	fmt.Printf("tree nodes:   %d\n", len(result.Synthesis.Tree))
	fmt.Printf("conflicts:    %d\n", len(result.Synthesis.Conflicts))
	fmt.Printf("messages:     %d\n", len(result.Synthesis.Messages))
	fmt.Printf("model calls:  %d\n", fake.calls)
	fmt.Printf("live:         %v\n", *live)
	if *outPath != "" {
		fmt.Printf("saved:        %s\n", *outPath)
	}
}
