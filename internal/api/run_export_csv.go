package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"activity-descriptor/internal/domain"
)

func writeRunCSV(w http.ResponseWriter, runID string, nodes []domain.Node) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", exportFilename(runID, "csv")))
	w.Header().Set("Cache-Control", "no-store")
	output := csv.NewWriter(w)
	if err := output.Write([]string{"node_id", "parent_id", "type", "title", "description", "evidence_id", "source_id", "ref_id", "quote", "status"}); err != nil {
		return
	}
	for _, node := range nodes {
		var parent string
		if node.ParentID != nil {
			parent = *node.ParentID
		}
		row := []string{node.ID, parent, string(node.Type), node.Title, node.Description, "", "", "", "", ""}
		if len(node.Evidence) == 0 {
			if err := writeCSVRow(output, row); err != nil {
				return
			}
		}
		for _, evidence := range node.Evidence {
			row[5], row[6], row[7], row[8], row[9] = evidence.ID, evidence.SourceID, evidence.RefID, evidence.Quote, string(evidence.Status)
			if err := writeCSVRow(output, row); err != nil {
				return
			}
		}
	}
	output.Flush()
}

func writeCSVRow(output *csv.Writer, row []string) error {
	for i, value := range row {
		trimmed := strings.TrimLeftFunc(value, func(r rune) bool { return unicode.IsSpace(r) || r == '\ufeff' })
		if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
			// При открытии файла таблица должна воспринимать эту ячейку как текст.
			row[i] = "'" + value
		}
	}
	return output.Write(row)
}
