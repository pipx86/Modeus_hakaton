package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"activity-descriptor/internal/store/db"
)

func TestОтчётИПриоритеты(t *testing.T) {
	rows := []db.ListTreeDataIssuesRow{{Code: "MISSING_EVIDENCE"}, {Code: "CYCLE_DETECTED"}, {Code: "UNKNOWN_CODE"}}
	r := makeReport(rows, time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC))
	if r.CriticalCount != 3 || len(r.Issues) != 3 {
		t.Fatalf("неверный отчёт: %+v", r)
	}
	path, err := saveReport(t.TempDir(), r)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got report
	if err = json.Unmarshal(contents, &got); err != nil {
		t.Fatal(err)
	}
	if got.CriticalCount != 3 || got.Issues[0].Code != "MISSING_EVIDENCE" {
		t.Fatalf("неверный JSON: %+v", got)
	}
}
func TestПустойОтчёт(t *testing.T) {
	r := makeReport(nil, time.Now())
	if r.CriticalCount != 0 || r.Issues == nil {
		t.Fatalf("пустой отчёт: %+v", r)
	}
}
