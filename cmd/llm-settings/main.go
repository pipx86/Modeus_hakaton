// Command llm-settings runs one agent over a small matrix of OpenAI-compatible
// provider settings (model, temperature, max tokens) and reports reproducible
// results: latency, schema validity, token usage and a truncated output preview.
//
// Live runs require LLM_API_KEY and cost provider tokens. The -dry-run mode
// prints the planned matrix and effective settings without any network call and
// is what CI and the package tests use.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llm/agents"
	"activity-descriptor/internal/llm/openai"
	"activity-descriptor/internal/prompts"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2

	// defaultRequestTimeout is the per-request timeout used when -timeout is not
	// set. It is intentionally independent of LLM_*_TIMEOUT_S so a timeout
	// experiment does not silently inherit the product setting.
	defaultRequestTimeout = 120 * time.Second

	// previewRunes limits the raw output stored in the JSON report; the table
	// truncates further so one result stays on one line.
	previewRunes      = 200
	tablePreviewRunes = 60
	tableErrorRunes   = 60

	// liveCostWarningThreshold is where we warn that a live matrix is getting
	// expensive (every cell is one paid provider call).
	liveCostWarningThreshold = 20

	// maxTemperature is the upper bound accepted by the provider configuration
	// (see internal/llm/agents).
	maxTemperature = 2.0

	// dryRunPlaceholderKey lets -dry-run derive provider defaults from
	// agents.FromEnv when no real key is configured. It only ever lives in the
	// process environment for the duration of that call: it is cleared before
	// returning and is never printed or persisted.
	dryRunPlaceholderKey = "llm-settings-dry-run-placeholder"

	// defaultTemperatures mirrors the flag default and is also used when the
	// flag is explicitly set to an empty value.
	defaultTemperatures = "0.2,0.4"
)

var defaultTemperatureValues = []float64{0.2, 0.4}

// combination is one point of the experiment matrix.
type combination struct {
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
}

// cellResult is one measured repetition of one combination.
type cellResult struct {
	Model            string  `json:"model"`
	Temperature      float64 `json:"temperature"`
	MaxTokens        int     `json:"max_tokens"`
	Repeat           int     `json:"repeat"`
	TimeoutMS        int64   `json:"timeout_ms"`
	Success          bool    `json:"success"`
	JSONValid        bool    `json:"json_valid"`
	DurationMS       int64   `json:"duration_ms"`
	Error            string  `json:"error,omitempty"`
	JSONError        string  `json:"json_error,omitempty"`
	Preview          string  `json:"preview,omitempty"`
	PromptTokens     int     `json:"prompt_tokens,omitempty"`
	CompletionTokens int     `json:"completion_tokens,omitempty"`
	TotalTokens      int     `json:"total_tokens,omitempty"`
}

// cellSummary aggregates the measured cells.
type cellSummary struct {
	Total         int   `json:"total"`
	Success       int   `json:"success"`
	JSONValid     int   `json:"json_valid"`
	Failed        int   `json:"failed"`
	AllFailed     bool  `json:"all_failed"`
	AvgDurationMS int64 `json:"avg_duration_ms"`
	TotalTokens   int   `json:"total_tokens"`
}

// report is the reproducible JSON artifact written by -out.
type report struct {
	DryRun           bool          `json:"dry_run"`
	StartedAt        string        `json:"started_at"`
	Agent            string        `json:"agent"`
	Contract         string        `json:"contract"`
	PromptVersion    string        `json:"prompt_version"`
	PromptSHA256     string        `json:"prompt_sha256"`
	BaseURL          string        `json:"base_url"`
	APIKeyConfigured bool          `json:"api_key_configured"`
	ReasoningEffort  string        `json:"reasoning_effort,omitempty"`
	RequestTimeoutMS int64         `json:"request_timeout_ms"`
	Repeats          int           `json:"repeats"`
	Plan             []combination `json:"plan"`
	PlannedRequests  int           `json:"planned_requests"`
	Cells            []cellResult  `json:"cells,omitempty"`
	Summary          *cellSummary  `json:"summary,omitempty"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the command and returns the process exit code. stdout and
// stderr are injected so tests can exercise the whole dry-run path without a
// process and without network access.
func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("llm-settings", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "usage: go run ./cmd/llm-settings [flags]")
		fmt.Fprintln(flags.Output(), "\nRuns one agent over a small matrix of model settings and reports latency,")
		fmt.Fprintln(flags.Output(), "schema validity, token usage and a truncated output preview for every cell.")
		fmt.Fprintln(flags.Output(), "\nLive runs require LLM_API_KEY and cost provider tokens.")
		fmt.Fprintln(flags.Output(), "Use -dry-run in CI: it prints the planned matrix and makes no network calls.")
		fmt.Fprintln(flags.Output(), "\nFlags:")
		flags.PrintDefaults()
	}

	agentFlag := flags.String("agent", "interviewer", "agent to exercise: interviewer or analyst")
	modelsFlag := flags.String("models", "", "comma-separated models (default: configured model for -agent)")
	temperaturesFlag := flags.String("temperatures", defaultTemperatures, "comma-separated temperatures between 0 and 2")
	maxTokensFlag := flags.String("max-tokens", "", "comma-separated max token limits (default: configured limit for -agent)")
	timeoutFlag := flags.Duration("timeout", defaultRequestTimeout, "timeout for each request, e.g. 120s")
	repeatFlag := flags.Int("repeat", 1, "repetitions per matrix cell")
	outFlag := flags.String("out", "", "optional path for the JSON report")
	strictFlag := flags.Bool("strict", false, "exit non-zero unless every call succeeds and returns schema-valid JSON")
	dryRunFlag := flags.Bool("dry-run", false, "print the planned matrix and effective settings without network calls")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "llm-settings: unexpected arguments: %s\n", strings.Join(flags.Args(), " "))
		return exitUsage
	}

	kind, agentName, err := parseAgent(*agentFlag)
	if err != nil {
		fmt.Fprintf(stderr, "llm-settings: %v\n", err)
		return exitUsage
	}
	if *timeoutFlag <= 0 {
		fmt.Fprintln(stderr, "llm-settings: -timeout must be positive")
		return exitUsage
	}
	if *repeatFlag < 1 {
		fmt.Fprintln(stderr, "llm-settings: -repeat must be at least 1")
		return exitUsage
	}

	temperatures, err := parseFloatList(*temperaturesFlag)
	if err != nil {
		fmt.Fprintf(stderr, "llm-settings: -temperatures: %v\n", err)
		return exitUsage
	}
	if len(temperatures) == 0 {
		temperatures = defaultTemperatureValues
	}
	models, err := parseModelList(*modelsFlag)
	if err != nil {
		fmt.Fprintf(stderr, "llm-settings: -models: %v\n", err)
		return exitUsage
	}
	maxTokens, err := parseIntList(*maxTokensFlag)
	if err != nil {
		fmt.Fprintf(stderr, "llm-settings: -max-tokens: %v\n", err)
		return exitUsage
	}

	cfg, apiKeyConfigured, err := loadProviderConfig(*dryRunFlag)
	if err != nil {
		fmt.Fprintf(stderr, "llm-settings: provider configuration: %v\n", err)
		fmt.Fprintln(stderr, "hint: set LLM_API_KEY (or OPENROUTER_API_KEY) for live runs; -dry-run needs no key and makes no calls.")
		return exitUsage
	}
	spec, ok := cfg.Agents[agentName]
	if !ok {
		fmt.Fprintf(stderr, "llm-settings: no configuration for agent %q\n", agentName)
		return exitUsage
	}
	if len(models) == 0 {
		models = []string{spec.Model}
	}
	if len(maxTokens) == 0 {
		maxTokens = []int{spec.MaxTokens}
	}

	messages, prompt, err := buildMessages(kind)
	if err != nil {
		fmt.Fprintf(stderr, "llm-settings: prompt: %v\n", err)
		return exitUsage
	}

	plan := buildPlan(models, temperatures, maxTokens)
	plannedRequests := len(plan) * *repeatFlag
	rep := report{
		DryRun:           *dryRunFlag,
		StartedAt:        time.Now().UTC().Format(time.RFC3339),
		Agent:            string(kind),
		Contract:         agent.ContractID,
		PromptVersion:    prompt.Version,
		PromptSHA256:     prompt.SHA256,
		BaseURL:          cfg.BaseURL,
		APIKeyConfigured: apiKeyConfigured,
		ReasoningEffort:  spec.ReasoningEffort,
		RequestTimeoutMS: timeoutFlag.Milliseconds(),
		Repeats:          *repeatFlag,
		Plan:             plan,
		PlannedRequests:  plannedRequests,
	}

	printHeader(stdout, rep)

	if *dryRunFlag {
		printPlan(stdout, rep)
		if !apiKeyConfigured {
			fmt.Fprintln(stderr, "llm-settings: LLM_API_KEY is not set; -dry-run only plans the matrix. A live run requires a key.")
		}
		if *outFlag != "" {
			if err := writeReport(*outFlag, rep); err != nil {
				fmt.Fprintf(stderr, "llm-settings: write report: %v\n", err)
				return exitFailure
			}
			fmt.Fprintf(stdout, "\nreport: %s\n", *outFlag)
		}
		return exitOK
	}

	if plannedRequests > liveCostWarningThreshold {
		fmt.Fprintf(stderr, "llm-settings: warning: %d live requests to %s, each one is a paid provider call\n",
			plannedRequests, cfg.BaseURL)
	}
	fmt.Fprintf(stdout, "\nrunning %d request(s) through %s...\n", plannedRequests, cfg.BaseURL)
	cells := executePlan(context.Background(), cfg, kind, spec.ReasoningEffort, plan, *timeoutFlag, *repeatFlag, messages, stdout)
	summary := summarize(cells)
	rep.Cells = cells
	rep.Summary = &summary

	fmt.Fprintln(stdout)
	printTable(stdout, cells)
	printSummary(stdout, summary)

	if *outFlag != "" {
		if err := writeReport(*outFlag, rep); err != nil {
			fmt.Fprintf(stderr, "llm-settings: write report: %v\n", err)
			return exitFailure
		}
		fmt.Fprintf(stdout, "\nreport: %s\n", *outFlag)
	}

	code := exitCodeFor(summary, *strictFlag)
	switch {
	case summary.AllFailed:
		fmt.Fprintf(stderr, "llm-settings: every cell failed (%d/%d)\n", summary.Failed, summary.Total)
	case code != exitOK:
		fmt.Fprintf(stderr, "llm-settings: -strict: %d/%d calls succeeded, %d/%d outputs valid\n",
			summary.Success, summary.Total, summary.JSONValid, summary.Total)
	}
	return code
}

// parseAgent maps the -agent flag to the agent contracts used by the product
// code. Only the agents with a stable output schema are supported.
func parseAgent(raw string) (agent.Kind, agents.AgentName, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(agent.KindInterviewer):
		return agent.KindInterviewer, agents.Interviewer, nil
	case string(agent.KindAnalyst):
		return agent.KindAnalyst, agents.Analyst, nil
	default:
		return "", "", fmt.Errorf("-agent must be interviewer or analyst, got %q", raw)
	}
}

// splitList turns "a, b,c" into trimmed items. An empty input yields no items;
// empty entries are an error so a typo does not silently drop a value.
func splitList(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			return nil, errors.New("empty value in comma-separated list")
		}
		items = append(items, item)
	}
	return items, nil
}

// parseModelList parses the -models flag. Duplicate models are removed while
// the original order is preserved.
func parseModelList(raw string) ([]string, error) {
	items, err := splitList(raw)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if seen[item] {
			continue
		}
		seen[item] = true
		models = append(models, item)
	}
	return models, nil
}

// parseFloatList parses the -temperatures flag. The provider configuration
// accepts temperatures in the [0, 2] range; NaN and Inf are rejected.
func parseFloatList(raw string) ([]float64, error) {
	items, err := splitList(raw)
	if err != nil {
		return nil, err
	}
	values := make([]float64, 0, len(items))
	seen := make(map[float64]bool, len(items))
	for _, item := range items {
		value, parseErr := strconv.ParseFloat(item, 64)
		if parseErr != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("invalid temperature %q", item)
		}
		if value < 0 || value > maxTemperature {
			return nil, fmt.Errorf("temperature %q is outside 0..%g", item, maxTemperature)
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		values = append(values, value)
	}
	return values, nil
}

// parseIntList parses the -max-tokens flag. Duplicates are removed and every
// value must be a positive integer.
func parseIntList(raw string) ([]int, error) {
	items, err := splitList(raw)
	if err != nil {
		return nil, err
	}
	values := make([]int, 0, len(items))
	seen := make(map[int]bool, len(items))
	for _, item := range items {
		value, parseErr := strconv.Atoi(item)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid max tokens value %q", item)
		}
		if value <= 0 {
			return nil, fmt.Errorf("max tokens %q must be positive", item)
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		values = append(values, value)
	}
	return values, nil
}

// buildPlan expands the matrix. Models are the outer loop, then temperatures,
// then max token limits, so the output order is stable across runs.
func buildPlan(models []string, temperatures []float64, maxTokens []int) []combination {
	plan := make([]combination, 0, len(models)*len(temperatures)*len(maxTokens))
	for _, model := range models {
		for _, temperature := range temperatures {
			for _, limit := range maxTokens {
				plan = append(plan, combination{
					Model:       model,
					Temperature: temperature,
					MaxTokens:   limit,
				})
			}
		}
	}
	return plan
}

// loadProviderConfig loads the provider configuration with agents.FromEnv.
// A missing API key is fatal for live runs; -dry-run tolerates it because
// planning only needs the effective defaults. The placeholder is never used
// for a network call and is restored before returning.
func loadProviderConfig(dryRun bool) (agents.Config, bool, error) {
	cfg, err := agents.FromEnv()
	if err == nil {
		return cfg, true, nil
	}
	if !dryRun {
		return agents.Config{}, false, err
	}

	previous, hadKey := os.LookupEnv("LLM_API_KEY")
	if setErr := os.Setenv("LLM_API_KEY", dryRunPlaceholderKey); setErr != nil {
		return agents.Config{}, false, err
	}
	cfg, retryErr := agents.FromEnv()
	if hadKey {
		_ = os.Setenv("LLM_API_KEY", previous)
	} else {
		_ = os.Unsetenv("LLM_API_KEY")
	}
	if retryErr != nil {
		return agents.Config{}, false, retryErr
	}
	return cfg, false, nil
}

// interviewerDemoInput is a realistic mid-interview state: one question about
// the role has already been asked and answered.
func interviewerDemoInput() agent.InterviewerInput {
	return agent.InterviewerInput{
		SessionID: "llm-settings-demo",
		Respondent: domain.Respondent{
			ID:         "resp-demo",
			Name:       "Иван",
			Role:       "Аналитик данных",
			Level:      "middle",
			Profession: "Аналитик данных",
		},
		Profession: "Аналитик данных",
		Asked: []domain.Question{{
			ID:        "q1",
			Level:     domain.QuestionRole,
			Text:      "Чем вы занимаетесь в роли аналитика данных и за что отвечаете?",
			Rationale: "нужно зафиксировать границы роли",
		}},
		Answers: []domain.Answer{{
			QuestionID: "q1",
			Text:       "Я аналитик данных, отвечаю за отчётность и анализ продаж.",
		}},
		NextLevel:    domain.QuestionFunction,
		MaxQuestions: 6,
	}
}

const analystDemoTranscript = `Интервьюер: Чем вы занимаетесь в роли аналитика данных и за что отвечаете?
Респондент: Я аналитик данных, отвечаю за отчётность и анализ продаж.
Интервьюер: Какие крупные направления работы входят в вашу роль?
Респондент: Мои основные функции — отчётность, анализ требований и визуализация.
Интервьюер: Какие конкретные задачи вы выполняете каждый день?
Респондент: Каждый день пишу SQL-запросы для отчётов по продажам, потом свожу их в дашборд.
Интервьюер: Какими инструментами и шагами вы пользуетесь?
Респондент: Использую PostgreSQL и DBeaver, пишу запросы и проверяю выгрузки.`

// analystDemoInput is a short realistic transcript with vacancy context.
func analystDemoInput() agent.AnalystInput {
	return agent.AnalystInput{
		SessionID:  "llm-settings-demo",
		Profession: "Аналитик данных",
		Transcript: analystDemoTranscript,
		Sources: []domain.Source{
			{ID: "src-interview", Type: domain.SourceInterview, Title: "Демо-интервью"},
			{
				ID:      "src-vacancy",
				Type:    domain.SourceVacancy,
				Title:   "Вакансия аналитика данных",
				Content: "Требования: SQL, BI-дашборды, проверка качества данных.",
			},
		},
	}
}

// buildMessages assembles the same prompt shape the product agent sends: the
// shared system prompt, few-shot examples and the agent input JSON. Prompt
// assembly lives in the agent packages; it is mirrored here on purpose because
// the experiment measures the raw model answer and validates it itself.
func buildMessages(kind agent.Kind) ([]llm.Message, prompts.Document, error) {
	switch kind {
	case agent.KindInterviewer:
		doc, err := prompts.Load(prompts.KindInterviewer, prompts.DefaultVersion)
		if err != nil {
			return nil, prompts.Document{}, fmt.Errorf("load interviewer prompt: %w", err)
		}
		payload, err := json.MarshalIndent(interviewerDemoInput(), "", "  ")
		if err != nil {
			return nil, prompts.Document{}, fmt.Errorf("marshal interviewer input: %w", err)
		}
		messages := []llm.Message{
			{Role: llm.RoleSystem, Content: doc.System},
			{Role: llm.RoleUser, Content: "# Examples\n\n" + doc.Examples},
			{
				Role: llm.RoleUser,
				Content: "Текущее состояние интервью (JSON):\n" + string(payload) +
					"\n\nВерни следующий вопрос по схеме interviewer.output.schema.json.",
			},
		}
		return messages, doc, nil
	case agent.KindAnalyst:
		doc, err := prompts.Load(prompts.KindAnalyst, prompts.DefaultVersion)
		if err != nil {
			return nil, prompts.Document{}, fmt.Errorf("load analyst prompt: %w", err)
		}
		payload, err := json.Marshal(analystDemoInput())
		if err != nil {
			return nil, prompts.Document{}, fmt.Errorf("marshal analyst input: %w", err)
		}
		messages := []llm.Message{
			{Role: llm.RoleSystem, Content: doc.System},
			{Role: llm.RoleUser, Content: "# Examples\n\n" + doc.Examples},
			{
				Role: llm.RoleUser,
				Content: "Вход аналитика (JSON):\n" + string(payload) +
					"\n\nВерни JSON по схеме analyst.output.schema.json. " +
					"Каждая цитата должна дословно присутствовать в transcript.",
			},
		}
		return messages, doc, nil
	default:
		return nil, prompts.Document{}, fmt.Errorf("unsupported agent %q (supported: interviewer, analyst)", kind)
	}
}

// executePlan runs every planned repetition sequentially and streams one line
// per result. Failures are recorded and never stop the remaining cells.
func executePlan(ctx context.Context, cfg agents.Config, kind agent.Kind, reasoning string, plan []combination, timeout time.Duration, repeats int, messages []llm.Message, stdout io.Writer) []cellResult {
	total := len(plan) * repeats
	results := make([]cellResult, 0, total)
	index := 0
	for _, combo := range plan {
		for repetition := 1; repetition <= repeats; repetition++ {
			index++
			result := runCell(ctx, cfg, kind, reasoning, combo, timeout, repetition, messages)
			results = append(results, result)
			printProgress(stdout, index, total, result)
		}
	}
	return results
}

// runCell sends one request with a dedicated OpenAI-compatible client, measures
// the elapsed time and validates the raw answer against the agent schema.
func runCell(parent context.Context, cfg agents.Config, kind agent.Kind, reasoning string, combo combination, timeout time.Duration, repetition int, messages []llm.Message) cellResult {
	result := cellResult{
		Model:       combo.Model,
		Temperature: combo.Temperature,
		MaxTokens:   combo.MaxTokens,
		Repeat:      repetition,
		TimeoutMS:   timeout.Milliseconds(),
	}

	temperature := combo.Temperature
	var usage llm.Usage
	client, err := openai.New(openai.Config{
		BaseURL:         cfg.BaseURL,
		APIKey:          cfg.APIKey,
		Model:           combo.Model,
		Timeout:         timeout,
		MaxTokens:       combo.MaxTokens,
		Temperature:     &temperature,
		ReasoningEffort: reasoning,
	}, openai.WithUsageObserver(func(observed llm.Usage) {
		usage = observed
	}))
	if err != nil {
		result.Error = redact(err.Error(), cfg.APIKey)
		return result
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	start := time.Now()
	answer, err := client.Chat(ctx, messages)
	result.DurationMS = time.Since(start).Milliseconds()
	result.PromptTokens = usage.PromptTokens
	result.CompletionTokens = usage.CompletionTokens
	result.TotalTokens = usage.TotalTokens
	if err != nil {
		result.Error = redact(err.Error(), cfg.APIKey)
		return result
	}

	result.Success = true
	result.Preview = truncateRunes(answer, previewRunes)
	if validateErr := agent.ValidateOutput(kind, []byte(answer)); validateErr != nil {
		result.JSONError = validateErr.Error()
	} else {
		result.JSONValid = true
	}
	return result
}

// summarize aggregates the measured cells.
func summarize(cells []cellResult) cellSummary {
	summary := cellSummary{Total: len(cells)}
	var duration int64
	for _, cell := range cells {
		if cell.Success {
			summary.Success++
		} else {
			summary.Failed++
		}
		if cell.JSONValid {
			summary.JSONValid++
		}
		duration += cell.DurationMS
		summary.TotalTokens += cell.TotalTokens
	}
	if summary.Total > 0 {
		summary.AvgDurationMS = duration / int64(summary.Total)
	}
	summary.AllFailed = summary.Total > 0 && summary.Success == 0
	return summary
}

// exitCodeFor implements the failure policy: a live run fails only when every
// cell failed, while -strict fails when any call failed or any output was not
// schema-valid.
func exitCodeFor(summary cellSummary, strict bool) int {
	if summary.AllFailed {
		return exitFailure
	}
	if strict && (summary.Success != summary.Total || summary.JSONValid != summary.Total) {
		return exitFailure
	}
	return exitOK
}

func printHeader(w io.Writer, rep report) {
	mode := "live"
	if rep.DryRun {
		mode = "dry-run (no network calls)"
	}
	fmt.Fprintf(w, "mode:            %s\n", mode)
	fmt.Fprintf(w, "agent:           %s\n", rep.Agent)
	fmt.Fprintf(w, "contract:        %s\n", rep.Contract)
	fmt.Fprintf(w, "prompt:          %s v%s (sha256 %s)\n", rep.Agent, promptVersionNumber(rep.PromptVersion), shortSHA(rep.PromptSHA256))
	fmt.Fprintf(w, "provider:        %s\n", rep.BaseURL)
	fmt.Fprintf(w, "api key:         %s\n", apiKeyLabel(rep.APIKeyConfigured))
	if rep.ReasoningEffort != "" {
		fmt.Fprintf(w, "reasoning:       %s\n", rep.ReasoningEffort)
	}
	fmt.Fprintf(w, "request timeout: %s\n", time.Duration(rep.RequestTimeoutMS)*time.Millisecond)
	fmt.Fprintf(w, "repeats:         %d\n", rep.Repeats)
	fmt.Fprintf(w, "plan:            %d settings x %d repeats = %d requests\n",
		len(rep.Plan), rep.Repeats, rep.PlannedRequests)
}

func printPlan(w io.Writer, rep report) {
	timeout := time.Duration(rep.RequestTimeoutMS) * time.Millisecond
	fmt.Fprintf(w, "\nplanned matrix (%d settings x %d repeats = %d requests, no network calls)\n",
		len(rep.Plan), rep.Repeats, rep.PlannedRequests)
	for i, combo := range rep.Plan {
		fmt.Fprintf(w, "  %2d. model=%s temperature=%.2f max_tokens=%d timeout=%s\n",
			i+1, combo.Model, combo.Temperature, combo.MaxTokens, timeout)
	}
}

func printProgress(w io.Writer, index, total int, result cellResult) {
	fmt.Fprintf(w, "[%d/%d] model=%s temperature=%.2f max_tokens=%d repeat=%d success=%v json_valid=%v duration=%dms tokens=%d",
		index, total, result.Model, result.Temperature, result.MaxTokens, result.Repeat,
		result.Success, result.JSONValid, result.DurationMS, result.TotalTokens)
	if result.Error != "" {
		fmt.Fprintf(w, " error=%q", truncateRunes(result.Error, tableErrorRunes))
	}
	if result.JSONError != "" {
		fmt.Fprintf(w, " json_error=%q", truncateRunes(result.JSONError, tableErrorRunes))
	}
	fmt.Fprintln(w)
}

func printTable(w io.Writer, cells []cellResult) {
	if len(cells) == 0 {
		return
	}
	fmt.Fprintln(w, "results:")
	table := newTableWriter(w)
	fmt.Fprintln(table, "MODEL\tTEMP\tMAX_TOKENS\tSUCCESS\tJSON\tDURATION_MS\tTOKENS\tERROR\tPREVIEW")
	for _, cell := range cells {
		fmt.Fprintf(table, "%s\t%.2f\t%d\t%v\t%v\t%d\t%d\t%s\t%s\n",
			cell.Model,
			cell.Temperature,
			cell.MaxTokens,
			cell.Success,
			cell.JSONValid,
			cell.DurationMS,
			cell.TotalTokens,
			tableErrorText(cell),
			tablePreviewText(cell),
		)
	}
	_ = table.Flush()
}

func printSummary(w io.Writer, summary cellSummary) {
	fmt.Fprintf(w, "\nsummary: cells=%d success=%d json_valid=%d failed=%d avg_duration=%dms total_tokens=%d all_failed=%v\n",
		summary.Total, summary.Success, summary.JSONValid, summary.Failed,
		summary.AvgDurationMS, summary.TotalTokens, summary.AllFailed)
}

func tableErrorText(cell cellResult) string {
	switch {
	case cell.Error != "":
		return oneLine(truncateRunes(cell.Error, tableErrorRunes))
	case cell.JSONError != "":
		return oneLine(truncateRunes("json: "+cell.JSONError, tableErrorRunes))
	default:
		return ""
	}
}

func tablePreviewText(cell cellResult) string {
	return oneLine(truncateRunes(cell.Preview, tablePreviewRunes))
}

func apiKeyLabel(configured bool) string {
	if configured {
		return "configured (hidden)"
	}
	return "not configured (dry-run only)"
}

func promptVersionNumber(version string) string {
	return strings.TrimPrefix(version, "v")
}

func shortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}

// newTableWriter returns the tab-aligned writer used for the result table.
func newTableWriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
}

// truncateRunes trims surrounding whitespace and returns at most limit runes.
func truncateRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// oneLine flattens a multi-line answer into a single table cell.
func oneLine(value string) string {
	replacer := strings.NewReplacer("\r\n", " ", "\n", " ", "\t", " ")
	return strings.TrimSpace(replacer.Replace(value))
}

// redact removes a provider key from an error string, just in case an upstream
// error echoes it back.
func redact(value, apiKey string) string {
	if apiKey == "" {
		return value
	}
	return strings.ReplaceAll(value, apiKey, "[redacted]")
}

func writeReport(path string, rep report) error {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
