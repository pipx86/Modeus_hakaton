package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
)

// WriteTable renders the human-readable result table, a one-line summary and
// the assertion failures of every failed case.
func WriteTable(w io.Writer, report Report) error {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "CASE\tSTATUS\tQ\tCLAR\tFALL\tNODES\tEVID\tCONF\tCALLS\tDURATION\tRESULT")
	for _, caseReport := range report.Cases {
		status := caseReport.Metrics.Status
		if caseReport.Error != "" {
			status = "error"
		}
		result := "PASS"
		if !caseReport.Passed {
			result = "FAIL"
		}
		fmt.Fprintf(writer, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\t%s\n",
			caseReport.Name,
			status,
			caseReport.Metrics.Questions,
			caseReport.Metrics.Clarifications,
			caseReport.Metrics.Fallbacks,
			caseReport.Metrics.TreeNodes,
			caseReport.Metrics.Evidence,
			caseReport.Metrics.Conflicts,
			caseReport.Metrics.ModelCalls,
			formatDuration(caseReport.Metrics.DurationMS),
			result,
		)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("eval: write table: %w", err)
	}

	total := report.Passed + report.Failed
	fmt.Fprintf(w, "\nSUMMARY: %d/%d cases passed", report.Passed, total)
	if total > 0 {
		var nodes, evidence int
		for _, caseReport := range report.Cases {
			nodes += caseReport.Metrics.TreeNodes
			evidence += caseReport.Metrics.Evidence
		}
		fmt.Fprintf(w, " | avg tree nodes %.1f | avg evidence %.1f", float64(nodes)/float64(total), float64(evidence)/float64(total))
	}
	fmt.Fprintf(w, " | total pipeline %s\n", formatDuration(report.TotalDurationMS))

	if report.Failed == 0 {
		return nil
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
	return nil
}

// WriteJSON writes the machine-readable report. The document contains no
// timestamps, so it is suitable for baseline diffs; duration values are
// informational and differ between machines.
func WriteJSON(w io.Writer, report Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("eval: marshal report: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("eval: write report: %w", err)
	}
	return nil
}

func formatDuration(milliseconds int64) string {
	if milliseconds >= 1000 {
		return fmt.Sprintf("%.2fs", float64(milliseconds)/1000)
	}
	return fmt.Sprintf("%dms", milliseconds)
}
