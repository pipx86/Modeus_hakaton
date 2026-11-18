package eval

import (
	"context"
	"encoding/json"
	"fmt"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/pipeline"
)

// Metrics is the measured output of one pipeline run. Only DurationMS and
// ContractValid are derived values; everything else is a count taken from the
// pipeline result.
type Metrics struct {
	Status           string         `json:"status"`
	ContractValid    bool           `json:"contract_valid"`
	Questions        int            `json:"questions"`
	Clarifications   int            `json:"clarifications"`
	Fallbacks        int            `json:"fallbacks"`
	Entities         int            `json:"entities"`
	TreeNodes        int            `json:"tree_nodes"`
	NodeTypes        map[string]int `json:"node_types"`
	Evidence         int            `json:"evidence"`
	Conflicts        int            `json:"conflicts"`
	Messages         int            `json:"messages"`
	ModelCalls       int            `json:"model_calls"`
	InterviewerCalls int            `json:"interviewer_calls"`
	AnalystCalls     int            `json:"analyst_calls"`
	DurationMS       int64          `json:"duration_ms"`
}

// CaseReport is the outcome of one golden case.
type CaseReport struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Profession  string   `json:"profession"`
	Passed      bool     `json:"passed"`
	// Error is set when the pipeline itself could not complete. A failing
	// assertion keeps the pipeline result and lists the failures below.
	Error    string   `json:"error,omitempty"`
	Failures []string `json:"failures,omitempty"`
	Metrics  Metrics  `json:"metrics"`
	Expect   Expect   `json:"expect"`
}

// Report is the result of one evaluation run. It is JSON-serializable and
// contains no timestamps, so the same fixtures produce the same report apart
// from informational duration values.
type Report struct {
	SchemaVersion   int          `json:"schema_version"`
	Passed          int          `json:"passed"`
	Failed          int          `json:"failed"`
	TotalDurationMS int64        `json:"total_duration_ms"`
	Cases           []CaseReport `json:"cases"`
}

// RunCase executes one golden case end-to-end through the real pipeline with the
// scripted model. It never touches the network and never panics on a case error:
// the problem is reported in CaseReport.Failures/Error.
func RunCase(ctx context.Context, tc Case) CaseReport {
	report := CaseReport{
		Name:        tc.Name,
		Description: tc.Description,
		Tags:        append([]string(nil), tc.Tags...),
		Profession:  tc.Profession,
		Expect:      tc.Expect,
	}
	result, client, err := execute(ctx, tc)
	if err != nil {
		report.Error = err.Error()
		report.Failures = []string{"pipeline: " + err.Error()}
		report.Metrics = Metrics{
			ModelCalls:       client.totalCalls(),
			InterviewerCalls: client.interviewerCalls,
			AnalystCalls:     client.analystCalls,
		}
		return report
	}
	report.Metrics = collectMetrics(result, client)
	report.Failures = CheckResult(tc, result)
	if tc.Expect.ModelCalls != nil && report.Metrics.ModelCalls != *tc.Expect.ModelCalls {
		report.Failures = append(report.Failures,
			fmt.Sprintf("model calls = %d, want %d", report.Metrics.ModelCalls, *tc.Expect.ModelCalls))
	}
	report.Passed = len(report.Failures) == 0
	return report
}

// execute replays one case through pipeline.Run and returns both the result and
// the scripted client that served the model calls.
func execute(ctx context.Context, tc Case) (pipeline.Result, *scriptClient, error) {
	client := newScriptClient(tc.Script)
	result, err := pipeline.Run(ctx, pipeline.Config{
		Client:     client,
		Respondent: tc.Respondent,
		Profession: tc.Profession,
		Answers:    tc.Answers.For,
		Sources:    tc.Sources,
		Vacancy:    tc.Vacancy,
		Standard:   tc.Standard,
	})
	return result, client, err
}

// RunAll executes every case in order and aggregates the report.
func RunAll(ctx context.Context, cases []Case) Report {
	report := Report{
		SchemaVersion: SchemaVersion,
		Cases:         make([]CaseReport, 0, len(cases)),
	}
	for _, tc := range cases {
		caseReport := RunCase(ctx, tc)
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

// Run executes the embedded golden set and returns its report.
func Run(ctx context.Context) (Report, error) {
	cases, err := DefaultCases()
	if err != nil {
		return Report{}, err
	}
	return RunAll(ctx, cases), nil
}

func (c *scriptClient) totalCalls() int {
	return c.interviewerCalls + c.analystCalls
}

func collectMetrics(result pipeline.Result, client *scriptClient) Metrics {
	metrics := Metrics{
		Status:           string(result.Run.Status),
		NodeTypes:        make(map[string]int),
		Entities:         len(result.Analyst.Entities),
		TreeNodes:        len(result.Synthesis.Tree),
		Conflicts:        len(result.Synthesis.Conflicts),
		Messages:         len(result.Synthesis.Messages),
		ModelCalls:       client.totalCalls(),
		InterviewerCalls: client.interviewerCalls,
		AnalystCalls:     client.analystCalls,
		DurationMS:       result.Duration.Milliseconds(),
	}
	for _, question := range result.Session.Questions {
		metrics.Questions++
		if question.Clarification {
			metrics.Clarifications++
		}
		if question.Fallback {
			metrics.Fallbacks++
		}
	}
	for _, node := range result.Synthesis.Tree {
		metrics.Evidence += len(node.Evidence)
		metrics.NodeTypes[string(node.Type)]++
	}
	metrics.ContractValid = contractsValid(result)
	return metrics
}

// contractsValid re-validates both model-facing outputs against the embedded
// agent schemas. The pipeline already validates during execution; recording it
// as a metric makes the JSON contract a first-class eval signal.
func contractsValid(result pipeline.Result) bool {
	analystJSON, err := json.Marshal(result.Analyst)
	if err != nil || agent.ValidateOutput(agent.KindAnalyst, analystJSON) != nil {
		return false
	}
	synthesisJSON, err := json.Marshal(result.Synthesis)
	if err != nil || agent.ValidateOutput(agent.KindSynthesizer, synthesisJSON) != nil {
		return false
	}
	return true
}
