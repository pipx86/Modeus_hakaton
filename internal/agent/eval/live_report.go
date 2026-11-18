package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// WriteLiveTable renders the human-readable live result: the provider plan, one
// row per case, observed model calls (including fallback models), a summary and
// the failures of every failed case.
func WriteLiveTable(w io.Writer, report LiveReport) error {
	writeLiveHeader(w, report.Provider, len(report.Cases), time.Duration(report.CaseTimeoutMS)*time.Millisecond)

	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "CASE\tSTATUS\tCONTRACT\tQ\tCLAR\tFALLQ\tNODES\tTYPES\tEVID\tCONF\tCALLS\tFB\tERR\tLATENCY\tRESULT")
	for _, caseReport := range report.Cases {
		status := caseReport.Metrics.Status
		if caseReport.Error != "" {
			status = "error"
		}
		result := "PASS"
		if !caseReport.Passed {
			result = "FAIL"
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%s\t%d\t%d\t%d\t%d\t%d\t%s\t%s\n",
			caseReport.Name,
			status,
			yesNo(caseReport.Metrics.ContractValid),
			caseReport.Metrics.Questions,
			caseReport.Metrics.Clarifications,
			caseReport.Metrics.FallbackQuestions,
			caseReport.Metrics.TreeNodes,
			formatNodeTypes(caseReport.Metrics.NodeTypes),
			caseReport.Metrics.Evidence,
			caseReport.Metrics.Conflicts,
			caseReport.Metrics.ModelCalls,
			caseReport.Metrics.FallbackCalls,
			caseReport.Metrics.ModelErrors,
			formatDuration(caseReport.Metrics.DurationMS),
			result,
		)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("eval: write live table: %w", err)
	}

	writeLiveModelCalls(w, report)
	writeLiveSummary(w, report)
	writeLiveFailures(w, report)
	return nil
}

// WriteLiveJSON writes the machine-readable live report. It contains provider
// model names but never the API key.
func WriteLiveJSON(w io.Writer, report LiveReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("eval: marshal live report: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("eval: write live report: %w", err)
	}
	return nil
}

// WriteLivePlan renders the configuration and case list without running
// anything. It backs the command's -dry-run mode, which validates the provider
// configuration without spending a single token.
func WriteLivePlan(w io.Writer, provider LiveProviderInfo, cases []LiveCase, timeout time.Duration) error {
	writeLiveHeader(w, provider, len(cases), timeout)
	names := make([]string, 0, len(cases))
	for _, tc := range cases {
		names = append(names, tc.Name)
	}
	fmt.Fprintf(w, "dry run: provider configuration is valid; no model calls were made\n")
	fmt.Fprintf(w, "planned cases: %s\n", strings.Join(names, ", "))
	return nil
}

func writeLiveHeader(w io.Writer, provider LiveProviderInfo, caseCount int, timeout time.Duration) {
	fmt.Fprintln(w, "LIVE AGENT PIPELINE EVAL (real provider)")
	if provider.BaseURL != "" {
		fmt.Fprintf(w, "provider: %s\n", provider.BaseURL)
	}
	agents := []string{"interviewer", "analyst"}
	for _, name := range agents {
		model := provider.Models[name]
		fallback := provider.FallbackModels[name]
		if model == "" && fallback == "" {
			continue
		}
		line := fmt.Sprintf("  %s: model=%s", name, valueOr(model, "<unset>"))
		if fallback != "" {
			line += ", fallback=" + fallback
		}
		fmt.Fprintln(w, line)
	}
	if provider.Synthesizer != "" {
		fmt.Fprintf(w, "  synthesizer: %s\n", provider.Synthesizer)
	}
	if timeout > 0 {
		fmt.Fprintf(w, "case timeout: %s\n", timeout.Round(time.Millisecond))
	}
	fmt.Fprintf(w, "cases: %d\n\n", caseCount)
}

func writeLiveSummary(w io.Writer, report LiveReport) {
	total := report.Passed + report.Failed
	contractValid := 0
	calls, errors, fallback := 0, 0, 0
	for _, caseReport := range report.Cases {
		if caseReport.Metrics.ContractValid {
			contractValid++
		}
		calls += caseReport.Metrics.ModelCalls
		errors += caseReport.Metrics.ModelErrors
		fallback += caseReport.Metrics.FallbackCalls
	}
	fmt.Fprintf(w, "\nSUMMARY: %d/%d cases passed | contract valid %d/%d | model calls %d (errors %d, fallback %d) | total %s\n",
		report.Passed, total, contractValid, total, calls, errors, fallback, formatDuration(report.TotalDurationMS))
}

// writeLiveModelCalls lists the configured primary and fallback models with the
// observed attempts, then any other model that actually answered (for example a
// fallback the provider config did not advertise).
func writeLiveModelCalls(w io.Writer, report LiveReport) {
	type modelStat struct {
		calls  int
		errors int
	}
	observed := make(map[string]map[string]*modelStat)
	for _, caseReport := range report.Cases {
		for _, call := range caseReport.Metrics.CallTrace {
			byModel := observed[call.Agent]
			if byModel == nil {
				byModel = make(map[string]*modelStat)
				observed[call.Agent] = byModel
			}
			stat := byModel[call.Model]
			if stat == nil {
				stat = &modelStat{}
				byModel[call.Model] = stat
			}
			stat.calls++
			if call.Error != "" {
				stat.errors++
			}
		}
	}
	if len(observed) == 0 {
		return
	}

	fmt.Fprintln(w, "\nMODEL CALLS")
	for _, agentName := range []string{"interviewer", "analyst"} {
		byModel := observed[agentName]
		primary := report.Provider.Models[agentName]
		fallback := report.Provider.FallbackModels[agentName]
		ordered := make([]string, 0, len(byModel)+2)
		seen := make(map[string]bool)
		for _, model := range []string{primary, fallback} {
			if model == "" || seen[model] {
				continue
			}
			seen[model] = true
			ordered = append(ordered, model)
		}
		extra := make([]string, 0)
		for model := range byModel {
			if !seen[model] {
				extra = append(extra, model)
			}
		}
		sort.Strings(extra)
		ordered = append(ordered, extra...)
		if len(ordered) == 0 {
			continue
		}
		fmt.Fprintf(w, "  %s:\n", agentName)
		for _, model := range ordered {
			stat := byModel[model]
			if stat == nil {
				stat = &modelStat{}
			}
			role := ""
			switch {
			case model == fallback:
				role = " (fallback)"
			case model != primary:
				role = " (unexpected model)"
			}
			fmt.Fprintf(w, "    %-28s %3d calls, %d errors%s\n", model, stat.calls, stat.errors, role)
		}
	}
}

func writeLiveFailures(w io.Writer, report LiveReport) {
	if report.Failed == 0 {
		return
	}
	fmt.Fprintln(w, "\nFAILURES")
	for _, caseReport := range report.Cases {
		if caseReport.Passed {
			continue
		}
		fmt.Fprintf(w, "- %s\n", caseReport.Name)
		if len(caseReport.Failures) == 0 && caseReport.Error != "" {
			fmt.Fprintf(w, "    pipeline: %s\n", caseReport.Error)
			continue
		}
		for _, failure := range caseReport.Failures {
			fmt.Fprintf(w, "    %s\n", failure)
		}
	}
}

func formatNodeTypes(types map[string]int) string {
	if len(types) == 0 {
		return "-"
	}
	names := make([]string, 0, len(types))
	for name := range types {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s:%d", name, types[name]))
	}
	return strings.Join(parts, ",")
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
