// Command temperature-smoke compares interviewer output across temperatures.
// It is a live diagnostic: the OpenRouter key must be present in the
// environment or in .secrets/openrouter.key.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/agent/interviewer"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llm/openai"
)

type result struct {
	Temperature      float64 `json:"temperature"`
	Run              int     `json:"run"`
	OK               bool    `json:"ok"`
	Error            string  `json:"error,omitempty"`
	Level            string  `json:"level,omitempty"`
	Question         string  `json:"question,omitempty"`
	Fallback         bool    `json:"fallback,omitempty"`
	LatencyMS        int64   `json:"latency_ms"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
}

type summary struct {
	Temperature  float64 `json:"temperature"`
	Runs         int     `json:"runs"`
	OK           int     `json:"ok"`
	Errors       int     `json:"errors"`
	Fallbacks    int     `json:"fallbacks"`
	Unique       int     `json:"unique_questions"`
	AvgLatencyMS int64   `json:"avg_latency_ms"`
	AvgTokens    int     `json:"avg_tokens"`
}

func main() {
	runs := flag.Int("runs", 3, "runs per temperature")
	temps := flag.String("temps", "0,0.4,0.8", "comma-separated temperatures")
	outPath := flag.String("out", "temperature-results.json", "raw results file")
	timeoutS := flag.Int("timeout", 120, "per-call timeout in seconds")
	flag.Parse()

	cfg, err := openai.ConfigFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	if cfg.Model == "" {
		cfg.Model = "nvidia/nemotron-3-ultra-550b-a55b:free"
	}
	cfg.Timeout = time.Duration(*timeoutS) * time.Second

	var results []result
	for _, rawTemp := range strings.Split(*temps, ",") {
		var temp float64
		if _, err := fmt.Sscanf(strings.TrimSpace(rawTemp), "%f", &temp); err != nil {
			fmt.Fprintf(os.Stderr, "bad temperature %q: %v\n", rawTemp, err)
			os.Exit(1)
		}
		var usage llm.Usage
		client, err := openai.New(openai.Config{
			BaseURL:     cfg.BaseURL,
			APIKey:      cfg.APIKey,
			Model:       cfg.Model,
			Timeout:     cfg.Timeout,
			MaxTokens:   1500,
			Temperature: &temp,
		}, openai.WithUsageObserver(func(u llm.Usage) {
			usage.PromptTokens += u.PromptTokens
			usage.CompletionTokens += u.CompletionTokens
			usage.TotalTokens += u.TotalTokens
		}))
		if err != nil {
			fmt.Fprintf(os.Stderr, "client for %.1f: %v\n", temp, err)
			os.Exit(1)
		}
		agentInstance, err := interviewer.New(client)
		if err != nil {
			fmt.Fprintf(os.Stderr, "interviewer for %.1f: %v\n", temp, err)
			os.Exit(1)
		}
		for i := 0; i < *runs; i++ {
			usage = llm.Usage{}
			r := runOne(context.Background(), agentInstance, temp, i+1, usage)
			results = append(results, r)
			printResult(r)
		}
	}

	for _, s := range summarize(results) {
		printSummary(s)
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal results: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*outPath, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write results: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nraw results: %s\n", *outPath)
}

func runOne(ctx context.Context, agentInstance *interviewer.Agent, temp float64, run int, usage llm.Usage) result {
	ctx, cancel := context.WithTimeout(ctx, 130*time.Second)
	defer cancel()
	start := time.Now()
	out, err := agentInstance.AskNext(ctx, agent.InterviewerInput{
		SessionID:    fmt.Sprintf("temp-%.1f-%d", temp, run),
		Respondent:   domain.Respondent{ID: "resp", Role: "Аналитик данных", Profession: "Аналитик данных"},
		Profession:   "Аналитик данных",
		NextLevel:    domain.QuestionRole,
		MaxQuestions: 1,
	})
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return result{Temperature: temp, Run: run, Error: err.Error(), LatencyMS: elapsed, PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens}
	}
	return result{
		Temperature:      temp,
		Run:              run,
		OK:               true,
		Level:            string(out.Question.Level),
		Question:         out.Question.Text,
		Fallback:         out.Question.Fallback,
		LatencyMS:        elapsed,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
	}
}

func summarize(results []result) []summary {
	byTemp := map[float64][]result{}
	for _, r := range results {
		byTemp[r.Temperature] = append(byTemp[r.Temperature], r)
	}
	temps := make([]float64, 0, len(byTemp))
	for temp := range byTemp {
		temps = append(temps, temp)
	}
	sort.Float64s(temps)
	out := make([]summary, 0, len(temps))
	for _, temp := range temps {
		items := byTemp[temp]
		s := summary{Temperature: temp, Runs: len(items)}
		unique := map[string]bool{}
		var latency, tokens int64
		for _, r := range items {
			latency += r.LatencyMS
			tokens += int64(r.TotalTokens)
			if r.OK {
				s.OK++
				unique[r.Question] = true
			} else {
				s.Errors++
			}
			if r.Fallback {
				s.Fallbacks++
			}
		}
		s.Unique = len(unique)
		if len(items) > 0 {
			s.AvgLatencyMS = latency / int64(len(items))
			s.AvgTokens = int(tokens / int64(len(items)))
		}
		out = append(out, s)
	}
	return out
}

func printResult(r result) {
	if r.OK {
		preview := r.Question
		if len([]rune(preview)) > 100 {
			preview = string([]rune(preview)[:100]) + "..."
		}
		fmt.Printf("temp=%.1f run=%d ok level=%s tokens=%d latency=%dms question=%q\n",
			r.Temperature, r.Run, r.Level, r.TotalTokens, r.LatencyMS, preview)
		return
	}
	fmt.Printf("temp=%.1f run=%d ERROR tokens=%d latency=%dms error=%s\n",
		r.Temperature, r.Run, r.TotalTokens, r.LatencyMS, r.Error)
}

func printSummary(s summary) {
	fmt.Printf("summary temp=%.1f runs=%d ok=%d errors=%d fallbacks=%d unique=%d avg_latency=%dms avg_tokens=%d\n",
		s.Temperature, s.Runs, s.OK, s.Errors, s.Fallbacks, s.Unique, s.AvgLatencyMS, s.AvgTokens)
}
