package eval

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llm/agents"
	"activity-descriptor/internal/pipeline"
)

// Live evaluation: the same interviewer → analyst → synthesizer pipeline as the
// offline harness in run.go, but with the real provider clients built by
// internal/llm/agents instead of the scripted client. It is a manual,
// non-CI check: it costs provider tokens and its output varies between runs.
//
// The live harness deliberately asserts only contract invariants. Counts such
// as the number of entities or tree nodes are recorded for comparison but never
// asserted, because a real model may legitimately produce a different tree. The
// one deterministic expectation a live case may carry is the number of resolved
// conflicts produced from its own standard/vacancy nodes.

const (
	// LiveSchemaVersion is the version of the live report document.
	LiveSchemaVersion = 1

	// DefaultLiveCaseTimeout bounds one whole case, including every model call,
	// retry and repair attempt. A live case has at most six interview questions
	// and a handful of analyst calls, so ten minutes is a hard upper bound
	// rather than an expected duration.
	DefaultLiveCaseTimeout = 10 * time.Minute

	// LiveCaseBaseline is the interview-only data analyst live case.
	LiveCaseBaseline = "data-analyst-baseline"
	// LiveCaseConflict is the hybrid data analyst live case whose standard and
	// vacancy nodes disagree on a function title.
	LiveCaseConflict = "data-analyst-standard-conflict"
)

// LiveCase is one live evaluation case: the pipeline input plus the deterministic
// expectations that do not depend on the model. It deliberately has no Script
// field: the answers are scripted, the model output is real.
type LiveCase struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Profession  string            `json:"profession"`
	Respondent  domain.Respondent `json:"respondent,omitempty"`
	Answers     Answers           `json:"answers"`
	Sources     []domain.Source   `json:"sources"`
	Standard    []domain.Node     `json:"standard,omitempty"`
	Vacancy     []domain.Node     `json:"vacancy,omitempty"`
	Expect      LiveExpect        `json:"expect,omitempty"`
}

// LiveExpect is the model-independent assertion set of a live case. Anything
// that a real model can vary is recorded as a metric instead of asserted.
type LiveExpect struct {
	// MinConflicts asserts that the synthesizer resolved at least this many
	// conflicts. It is deterministic only for cases that feed conflicting
	// standard/vacancy nodes, so zero disables the assertion.
	MinConflicts int `json:"min_conflicts,omitempty"`
}

// LiveProviderInfo is the provider configuration reported with a live run. It
// never contains the API key.
type LiveProviderInfo struct {
	BaseURL        string            `json:"base_url,omitempty"`
	Models         map[string]string `json:"models,omitempty"`
	FallbackModels map[string]string `json:"fallback_models,omitempty"`
	Synthesizer    string            `json:"synthesizer,omitempty"`
}

// LiveClients is the real per-agent client set used by the live harness. The
// Recorder is optional; when set, every provider attempt of every model
// (primary and fallback) is recorded, which is how fallback usage is measured.
type LiveClients struct {
	Interviewer llm.ChatClient
	Analyst     llm.ChatClient
	Recorder    *CallRecorder
	// Models is the configured primary model per agent. Calls answered by any
	// other model are reported as fallback calls.
	Models map[string]string
}

// ModelCall is one recorded provider attempt. Error is empty on success and is
// redacted before it reaches a report.
type ModelCall struct {
	Agent      string `json:"agent"`
	Model      string `json:"model"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// CallRecorder collects provider attempts. It is safe for concurrent use: the
// pool's gate may run several calls of one agent through different models.
type CallRecorder struct {
	mu    sync.Mutex
	calls []ModelCall
}

// NewCallRecorder returns an empty recorder.
func NewCallRecorder() *CallRecorder {
	return &CallRecorder{}
}

// Observe records one attempt. Implementations of RecordingDecorator call it
// after the model client returned.
func (r *CallRecorder) Observe(agentName, model string, duration time.Duration, err error) {
	if r == nil {
		return
	}
	call := ModelCall{
		Agent:      agentName,
		Model:      model,
		DurationMS: duration.Milliseconds(),
	}
	if err != nil {
		call.Error = err.Error()
	}
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
}

// Len reports how many attempts have been recorded.
func (r *CallRecorder) Len() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// Calls returns a copy of every recorded attempt.
func (r *CallRecorder) Calls() []ModelCall {
	return r.CallsSince(0)
}

// CallsSince returns a copy of the attempts recorded at or after index. An
// out-of-range index is clamped, so a case can snapshot the shared recorder
// before its run and read only its own attempts afterwards.
func (r *CallRecorder) CallsSince(index int) []ModelCall {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if index < 0 {
		index = 0
	}
	if index > len(r.calls) {
		index = len(r.calls)
	}
	calls := make([]ModelCall, len(r.calls)-index)
	copy(calls, r.calls[index:])
	return calls
}

// Wrap returns client decorated so every Chat call is recorded as an attempt of
// agent on model. A nil recorder or client is returned unchanged.
func (r *CallRecorder) Wrap(agentName, model string, client llm.ChatClient) llm.ChatClient {
	if r == nil || client == nil {
		return client
	}
	return &recordedClient{recorder: r, agent: agentName, model: model, client: client}
}

// RecordingDecorator adapts a CallRecorder to agents.WithClientDecorator. It is
// attached to the pool, so it sees the primary client of every agent and the
// optional fallback client as separate models.
func RecordingDecorator(recorder *CallRecorder) agents.ClientDecorator {
	return func(name agents.AgentName, model string, client llm.ChatClient) llm.ChatClient {
		return recorder.Wrap(string(name), model, client)
	}
}

type recordedClient struct {
	recorder *CallRecorder
	agent    string
	model    string
	client   llm.ChatClient
}

// Chat implements llm.ChatClient.
func (c *recordedClient) Chat(ctx context.Context, messages []llm.Message) (string, error) {
	start := time.Now()
	answer, err := c.client.Chat(ctx, messages)
	c.recorder.Observe(c.agent, c.model, time.Since(start), err)
	return answer, err
}

// LiveRunner executes live cases against configured provider clients.
type LiveRunner struct {
	Clients  LiveClients
	Provider LiveProviderInfo
	// CaseTimeout bounds one case. Zero means DefaultLiveCaseTimeout.
	CaseTimeout time.Duration
	// RedactSecrets are literal strings (for example the API key) replaced by
	// "[REDACTED]" in every recorded error before it reaches a report.
	RedactSecrets []string
}

// LiveCaseReport is the outcome of one live case. Error is set only when the
// pipeline itself could not complete (provider failure, timeout); a failing
// contract assertion keeps the result and lists the failures.
type LiveCaseReport struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Tags        []string    `json:"tags,omitempty"`
	Profession  string      `json:"profession"`
	Passed      bool        `json:"passed"`
	Error       string      `json:"error,omitempty"`
	Failures    []string    `json:"failures,omitempty"`
	Metrics     LiveMetrics `json:"metrics"`
}

// LiveMetrics is the measured output of one live run. Values derived from the
// actual model output are observations, not assertions.
type LiveMetrics struct {
	Status            string         `json:"status"`
	ContractValid     bool           `json:"contract_valid"`
	Questions         int            `json:"questions"`
	Clarifications    int            `json:"clarifications"`
	FallbackQuestions int            `json:"fallback_questions"`
	Entities          int            `json:"entities"`
	TreeNodes         int            `json:"tree_nodes"`
	NodeTypes         map[string]int `json:"node_types"`
	Evidence          int            `json:"evidence"`
	Conflicts         int            `json:"conflicts"`
	Messages          int            `json:"messages"`
	DurationMS        int64          `json:"duration_ms"`
	// ModelCalls counts provider attempts (primary and fallback, retries and
	// repairs included), not logical agent calls.
	ModelCalls     int            `json:"model_calls"`
	ModelErrors    int            `json:"model_errors"`
	FallbackCalls  int            `json:"fallback_calls"`
	FallbackServed int            `json:"fallback_served"`
	Models         []string       `json:"models"`
	CallsByAgent   map[string]int `json:"calls_by_agent,omitempty"`
	CallsByModel   map[string]int `json:"calls_by_model,omitempty"`
	FirstError     string         `json:"first_error,omitempty"`
	CallTrace      []ModelCall    `json:"call_trace,omitempty"`
}

// LiveReport is the result of one live evaluation run.
type LiveReport struct {
	SchemaVersion   int              `json:"schema_version"`
	Mode            string           `json:"mode"`
	Provider        LiveProviderInfo `json:"provider"`
	CaseTimeoutMS   int64            `json:"case_timeout_ms"`
	Passed          int              `json:"passed"`
	Failed          int              `json:"failed"`
	TotalDurationMS int64            `json:"total_duration_ms"`
	Cases           []LiveCaseReport `json:"cases"`
}

// Validate checks that a live case is complete and internally consistent. It
// also verifies that every provided standard/vacancy node references a declared
// source of the matching type, so the hybrid synthesizer input is truthful.
func (c LiveCase) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("live: name is required")
	}
	if strings.TrimSpace(c.Profession) == "" {
		return fmt.Errorf("live: case %q: profession is required", c.Name)
	}
	for _, level := range []struct {
		name string
		text string
	}{
		{"role", c.Answers.Role},
		{"function", c.Answers.Function},
		{"task", c.Answers.Task},
		{"action", c.Answers.Action},
	} {
		if strings.TrimSpace(level.text) == "" {
			return fmt.Errorf("live: case %q: answers.%s is required", c.Name, level.name)
		}
	}
	if len(c.Sources) == 0 {
		return fmt.Errorf("live: case %q: at least one source is required", c.Name)
	}
	sourceTypes := make(map[string]domain.SourceType, len(c.Sources))
	for _, source := range c.Sources {
		if strings.TrimSpace(source.ID) == "" {
			return fmt.Errorf("live: case %q: source with empty id", c.Name)
		}
		if !validLiveSourceType(source.Type) {
			return fmt.Errorf("live: case %q: source %q has invalid type %q", c.Name, source.ID, source.Type)
		}
		if _, duplicate := sourceTypes[source.ID]; duplicate {
			return fmt.Errorf("live: case %q: duplicate source id %q", c.Name, source.ID)
		}
		sourceTypes[source.ID] = source.Type
	}
	if err := validateLiveNodes(c.Name, "standard", c.Standard, sourceTypes, domain.SourceStandard); err != nil {
		return err
	}
	if err := validateLiveNodes(c.Name, "vacancy", c.Vacancy, sourceTypes, domain.SourceVacancy); err != nil {
		return err
	}
	if c.Expect.MinConflicts < 0 {
		return fmt.Errorf("live: case %q: expect.min_conflicts must be >= 0", c.Name)
	}
	return nil
}

func validLiveSourceType(sourceType domain.SourceType) bool {
	switch sourceType {
	case domain.SourceInterview, domain.SourceStandard, domain.SourceVacancy, domain.SourceDocument:
		return true
	default:
		return false
	}
}

func validateLiveNodes(caseName, label string, nodes []domain.Node, sourceTypes map[string]domain.SourceType, want domain.SourceType) error {
	for i, node := range nodes {
		if strings.TrimSpace(node.ID) == "" {
			return fmt.Errorf("live: case %q: %s[%d] has empty id", caseName, label, i)
		}
		if !node.Type.Valid() {
			return fmt.Errorf("live: case %q: %s[%d] has invalid type %q", caseName, label, i, node.Type)
		}
		if strings.TrimSpace(node.Title) == "" {
			return fmt.Errorf("live: case %q: %s[%d] has empty title", caseName, label, i)
		}
		if len(node.Evidence) == 0 {
			return fmt.Errorf("live: case %q: %s[%d] has no evidence", caseName, label, i)
		}
		for j, evidence := range node.Evidence {
			if err := evidence.Validate(); err != nil {
				return fmt.Errorf("live: case %q: %s[%d].evidence[%d]: %w", caseName, label, i, j, err)
			}
			if got := sourceTypes[evidence.SourceID]; got != want {
				return fmt.Errorf("live: case %q: %s[%d].evidence[%d] references source %q of type %q, want a declared %q source",
					caseName, label, i, j, evidence.SourceID, got, want)
			}
		}
	}
	return nil
}

// offlineCase converts the live case to the offline Case shape so the existing
// invariant checks (contract, hierarchy, evidence, verbatim quotes, conflicts)
// are reused instead of reimplemented.
func (c LiveCase) offlineCase() Case {
	return Case{
		Name:       c.Name,
		Profession: c.Profession,
		Respondent: c.Respondent,
		Answers:    c.Answers,
		Sources:    c.Sources,
		Standard:   c.Standard,
		Vacancy:    c.Vacancy,
	}
}

// DefaultLiveCases returns the small, fixed live set. It is intentionally two
// cases: an interview-only baseline and a hybrid standard/vacancy conflict case.
func DefaultLiveCases() []LiveCase {
	return []LiveCase{
		{
			Name:        LiveCaseBaseline,
			Description: "Live interview-only data analyst case: real interviewer and analyst models, scripted respondent answers.",
			Tags:        []string{"live", "data-analyst", "baseline"},
			Profession:  "Аналитик данных",
			Respondent: domain.Respondent{
				ID:         "resp-live-da-1",
				Name:       "Демо-аналитик",
				Role:       "Аналитик данных",
				Level:      "middle",
				Profession: "Аналитик данных",
			},
			Answers: Answers{
				Role:     "Я аналитик данных, отвечаю за отчётность и качество данных в продуктовой команде.",
				Function: "Моя функция — отчётность и визуализация: готовлю регулярные отчёты и дашборды.",
				Task:     "Пишу SQL-запросы к хранилищу и строю дашборды для продуктовых команд.",
				Action:   "Оптимизирую SQL-запросы и проверяю выгрузки перед публикацией отчёта.",
			},
			Sources: []domain.Source{
				{ID: "src-interview", Type: domain.SourceInterview, Title: "Интервью аналитика данных"},
			},
		},
		{
			Name:        LiveCaseConflict,
			Description: "Live hybrid data analyst case: standard and vacancy contents are attached, and their nodes disagree on a function title.",
			Tags:        []string{"live", "data-analyst", "hybrid", "conflict"},
			Profession:  "Аналитик данных",
			Respondent: domain.Respondent{
				ID:         "resp-live-da-2",
				Name:       "Демо-аналитик",
				Role:       "Аналитик данных",
				Level:      "senior",
				Profession: "Аналитик данных",
			},
			Answers: Answers{
				Role:     "Я аналитик данных, отвечаю за отчётность и качество данных в продуктовой команде.",
				Function: "Моя функция — отчётность и визуализация: готовлю регулярные отчёты и дашборды.",
				Task:     "Пишу SQL-запросы к хранилищу и строю дашборды для продуктовых команд.",
				Action:   "Оптимизирую SQL-запросы и проверяю выгрузки перед публикацией отчёта.",
			},
			Sources: []domain.Source{
				{ID: "src-interview", Type: domain.SourceInterview, Title: "Интервью аналитика данных"},
				{
					ID:      "src-standard",
					Type:    domain.SourceStandard,
					Title:   "Профстандарт аналитика данных",
					Content: "Анализ и формализация требований. Подготовка и согласование функциональных требований. Ведение реестра требований.",
				},
				{
					ID:      "src-vacancy",
					Type:    domain.SourceVacancy,
					Title:   "Вакансия аналитика данных",
					Content: "Сбор и подготовка данных. Построение отчётов и дашбордов. Контроль качества данных.",
				},
			},
			Standard: []domain.Node{{
				ID:    "fn-shared",
				Type:  domain.NodeFunction,
				Title: "Анализ требований",
				Evidence: []domain.Evidence{{
					ID:       "ev-std-live-1",
					SourceID: "src-standard",
					RefID:    "std-3.1",
					Quote:    "Анализ и формализация требований",
				}},
			}},
			Vacancy: []domain.Node{{
				ID:    "fn-shared",
				Type:  domain.NodeFunction,
				Title: "Сбор данных",
				Evidence: []domain.Evidence{{
					ID:       "ev-vac-live-1",
					SourceID: "src-vacancy",
					RefID:    "vac-req-2",
					Quote:    "Сбор и подготовка данных",
				}},
			}},
			Expect: LiveExpect{MinConflicts: 1},
		},
	}
}

// timeout returns the effective per-case timeout.
func (r LiveRunner) timeout() time.Duration {
	if r.CaseTimeout <= 0 {
		return DefaultLiveCaseTimeout
	}
	return r.CaseTimeout
}

// RunAll executes every case in order and aggregates the report. Cases are
// sequential on purpose: two live cases already exercise the provider quota and
// sequencing makes the latency and call counts comparable.
func (r LiveRunner) RunAll(ctx context.Context, cases []LiveCase) LiveReport {
	report := LiveReport{
		SchemaVersion: LiveSchemaVersion,
		Mode:          "live",
		Provider:      r.Provider,
		CaseTimeoutMS: r.timeout().Milliseconds(),
		Cases:         make([]LiveCaseReport, 0, len(cases)),
	}
	for _, tc := range cases {
		caseReport := r.RunCase(ctx, tc)
		if caseReport.Passed {
			report.Passed++
		} else {
			report.Failed++
		}
		report.TotalDurationMS += caseReport.Metrics.DurationMS
		report.Cases = append(report.Cases, caseReport)
	}
	return report
}

// RunCase executes one live case through the real pipeline. It never panics and
// never fabricates a result: a provider failure or timeout is reported in
// LiveCaseReport.Error, and the case is not marked as passed.
func (r LiveRunner) RunCase(ctx context.Context, tc LiveCase) LiveCaseReport {
	report := LiveCaseReport{
		Name:        tc.Name,
		Description: tc.Description,
		Tags:        append([]string(nil), tc.Tags...),
		Profession:  tc.Profession,
	}
	if r.Clients.Interviewer == nil || r.Clients.Analyst == nil {
		report.Error = "live: interviewer and analyst clients are required"
		report.Failures = []string{report.Error}
		return report
	}
	if ctx == nil {
		ctx = context.Background()
	}

	timeout := r.timeout()
	caseCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	recorderIndex := 0
	if r.Clients.Recorder != nil {
		recorderIndex = r.Clients.Recorder.Len()
	}
	start := time.Now()
	result, err := pipeline.Run(caseCtx, pipeline.Config{
		InterviewerClient: r.Clients.Interviewer,
		AnalystClient:     r.Clients.Analyst,
		Respondent:        tc.Respondent,
		Profession:        tc.Profession,
		Answers:           tc.Answers.For,
		Sources:           tc.Sources,
		Vacancy:           tc.Vacancy,
		Standard:          tc.Standard,
	})
	elapsed := time.Since(start)

	var calls []ModelCall
	if r.Clients.Recorder != nil {
		calls = r.redactCalls(r.Clients.Recorder.CallsSince(recorderIndex))
	}
	if err != nil {
		report.Error = r.redact(err.Error())
		if errors.Is(caseCtx.Err(), context.DeadlineExceeded) {
			report.Error = fmt.Sprintf("case timeout after %s: %s", timeout, report.Error)
		}
		report.Failures = []string{"pipeline: " + report.Error}
		report.Metrics = liveMetricsFromCalls(calls, r.Clients.Models)
		report.Metrics.DurationMS = elapsed.Milliseconds()
		return report
	}

	report.Metrics = collectLiveMetrics(result, calls, r.Clients.Models)
	report.Metrics.DurationMS = result.Duration.Milliseconds()

	checkCase := tc.offlineCase()
	minEvidencePerNode := 1
	checkCase.Expect = Expect{MinEvidencePerNode: &minEvidencePerNode}
	report.Failures = CheckResult(checkCase, result)
	if tc.Expect.MinConflicts > 0 && len(result.Synthesis.Conflicts) < tc.Expect.MinConflicts {
		report.Failures = append(report.Failures, fmt.Sprintf(
			"conflicts = %d, want at least %d (deterministic from the provided standard/vacancy nodes)",
			len(result.Synthesis.Conflicts), tc.Expect.MinConflicts))
	}
	report.Passed = len(report.Failures) == 0
	return report
}

// collectLiveMetrics turns one completed pipeline result plus the recorded
// provider attempts into the live metrics.
func collectLiveMetrics(result pipeline.Result, calls []ModelCall, primaryModels map[string]string) LiveMetrics {
	metrics := LiveMetrics{
		Status:        string(result.Run.Status),
		NodeTypes:     make(map[string]int),
		Entities:      len(result.Analyst.Entities),
		TreeNodes:     len(result.Synthesis.Tree),
		Conflicts:     len(result.Synthesis.Conflicts),
		Messages:      len(result.Synthesis.Messages),
		ContractValid: contractsValid(result),
	}
	for _, question := range result.Session.Questions {
		metrics.Questions++
		if question.Clarification {
			metrics.Clarifications++
		}
		if question.Fallback {
			metrics.FallbackQuestions++
		}
	}
	for _, node := range result.Synthesis.Tree {
		metrics.Evidence += len(node.Evidence)
		metrics.NodeTypes[string(node.Type)]++
	}
	applyCallMetrics(&metrics, calls, primaryModels)
	return metrics
}

func liveMetricsFromCalls(calls []ModelCall, primaryModels map[string]string) LiveMetrics {
	metrics := LiveMetrics{NodeTypes: make(map[string]int)}
	applyCallMetrics(&metrics, calls, primaryModels)
	return metrics
}

// applyCallMetrics aggregates the recorded provider attempts. FallbackCalls
// counts every attempt served by a non-primary model (including a failed one);
// FallbackServed counts only the successful fallback attempts.
func applyCallMetrics(metrics *LiveMetrics, calls []ModelCall, primaryModels map[string]string) {
	metrics.ModelCalls = len(calls)
	metrics.CallsByAgent = make(map[string]int)
	metrics.CallsByModel = make(map[string]int)
	seenModels := make(map[string]bool)
	for _, call := range calls {
		metrics.CallsByAgent[call.Agent]++
		metrics.CallsByModel[call.Model]++
		seenModels[call.Model] = true
		primary, configured := primaryModels[call.Agent]
		isFallback := configured && primary != "" && call.Model != primary
		if isFallback {
			metrics.FallbackCalls++
		}
		if call.Error != "" {
			metrics.ModelErrors++
			if metrics.FirstError == "" {
				metrics.FirstError = call.Error
			}
			continue
		}
		if isFallback {
			metrics.FallbackServed++
		}
	}
	metrics.Models = make([]string, 0, len(seenModels))
	for model := range seenModels {
		metrics.Models = append(metrics.Models, model)
	}
	sort.Strings(metrics.Models)
	metrics.CallTrace = calls
}

// redact replaces every configured secret in value.
func (r LiveRunner) redact(value string) string {
	for _, secret := range r.RedactSecrets {
		secret = strings.TrimSpace(secret)
		if secret == "" {
			continue
		}
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	return value
}

func (r LiveRunner) redactCalls(calls []ModelCall) []ModelCall {
	out := make([]ModelCall, len(calls))
	for i, call := range calls {
		call.Error = truncateMessage(r.redact(call.Error), 500)
		out[i] = call
	}
	return out
}

func truncateMessage(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "...[truncated]"
}

// LiveCaseByName returns the built-in live case with the given name.
func LiveCaseByName(name string) (LiveCase, bool) {
	for _, tc := range DefaultLiveCases() {
		if tc.Name == name {
			return tc, true
		}
	}
	return LiveCase{}, false
}

// ValidateLiveCases validates every case and rejects duplicate names. The
// command calls it once before any provider request is made.
func ValidateLiveCases(cases []LiveCase) error {
	seen := make(map[string]bool, len(cases))
	for _, tc := range cases {
		if seen[tc.Name] {
			return fmt.Errorf("live: duplicate case name %q", tc.Name)
		}
		seen[tc.Name] = true
		if err := tc.Validate(); err != nil {
			return err
		}
	}
	return nil
}
