// Command nemotron-smoke sends one request per product agent in parallel to the
// configured OpenRouter/Nemotron endpoint. It verifies the key, the model and
// the shared concurrency/rate-limit gate without touching the database.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llm/agents"
)

type prompt struct {
	agent  agents.AgentName
	system string
	user   string
}

func main() {
	parallel := flag.Bool("parallel", true, "run the three agent calls concurrently")
	timeoutS := flag.Int("timeout", 240, "overall smoke timeout in seconds")
	showMetrics := flag.Bool("metrics", false, "print per-agent metrics after the smoke run")
	flag.Parse()

	cfg, err := agents.FromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		fmt.Fprintln(os.Stderr, "Put the key in .secrets/openrouter.key (git-ignored) or set OPENROUTER_API_KEY.")
		os.Exit(1)
	}
	pool, err := agents.NewPool(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pool: %v\n", err)
		os.Exit(1)
	}

	prompts := []prompt{
		{
			agent:  agents.Interviewer,
			system: "Ты интервьюер. За один ход задай РОВНО ОДИН открытый вопрос. Обращайся на «вы». Не более 25 слов. Верни только JSON: {\"question\":{\"id\":\"q\",\"level\":\"role\",\"text\":\"...\"},\"rationale\":\"...\",\"is_clarification\":false}.",
			user:   "Профессия: аналитик данных. Задай первый вопрос про роль.",
		},
		{
			agent:  agents.Analyst,
			system: "Ты аналитик. Извлеки из текста функции, задачи и действия. Каждая сущность обязана иметь дословную цитату. Верни только JSON с полем entities.",
			user:   "Текст: Аналитик готовит отчёт по воронке продаж и проверяет расхождения в данных.",
		},
		{
			agent:  agents.Synthesizer,
			system: "Ты синтезатор. Собери дерево роль → функции → задачи → действия. У каждого узла должна быть цитата. Верни только JSON.",
			user:   "Интервью: аналитик готовит отчёт. Стандарт: аналитик анализирует данные.",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutS)*time.Second)
	defer cancel()

	fmt.Printf("provider: %s\n", cfg.BaseURL)
	for _, name := range agents.Names() {
		spec := cfg.Agents[name]
		fmt.Printf("%-12s model=%s temperature=%.2f max_tokens=%d timeout=%s\n",
			name, spec.Model, spec.Temperature, spec.MaxTokens, spec.Timeout)
	}
	fmt.Printf("concurrency=%d rate=%d/min retries=%d parallel=%v\n\n",
		cfg.MaxConcurrency, cfg.RequestsPerMinute, cfg.RetryMax, *parallel)

	start := time.Now()
	type outcome struct {
		agent  agents.AgentName
		answer string
		err    error
		took   time.Duration
	}
	outcomes := make([]outcome, len(prompts))
	var wg sync.WaitGroup
	runOne := func(index int) {
		defer wg.Done()
		p := prompts[index]
		client, err := pool.Client(p.agent)
		if err != nil {
			outcomes[index] = outcome{agent: p.agent, err: err}
			return
		}
		began := time.Now()
		answer, err := client.Chat(ctx, []llm.Message{
			{Role: llm.RoleSystem, Content: p.system},
			{Role: llm.RoleUser, Content: p.user},
		})
		outcomes[index] = outcome{agent: p.agent, answer: answer, err: err, took: time.Since(began)}
	}

	if *parallel {
		for i := range prompts {
			wg.Add(1)
			go runOne(i)
		}
		wg.Wait()
	} else {
		for i := range prompts {
			wg.Add(1)
			runOne(i)
			wg.Wait()
		}
	}

	failures := 0
	for _, out := range outcomes {
		status := "ok"
		if out.err != nil {
			status = "ERROR: " + out.err.Error()
			failures++
		}
		preview := strings.TrimSpace(out.answer)
		if len([]rune(preview)) > 160 {
			preview = string([]rune(preview)[:160]) + "…"
		}
		preview = strings.ReplaceAll(preview, "\n", " ")
		fmt.Printf("%-12s %-8s %s\n", out.agent, status, out.took.Round(time.Millisecond))
		if out.err == nil {
			fmt.Printf("             %s\n", preview)
		}
	}
	fmt.Printf("\ntotal: %s, failures: %d\n", time.Since(start).Round(time.Millisecond), failures)
	if *showMetrics && pool.Metrics() != nil {
		fmt.Println("\n# agent metrics")
		_ = pool.Metrics().WritePrometheus(os.Stdout)
	}
	if failures > 0 {
		os.Exit(1)
	}
}
