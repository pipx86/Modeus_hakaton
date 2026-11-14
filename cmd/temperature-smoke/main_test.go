package main

import "testing"

func TestSummarize(t *testing.T) {
	results := []result{
		{Temperature: 0, Run: 1, OK: true, Question: "q1", LatencyMS: 100, TotalTokens: 10},
		{Temperature: 0, Run: 2, OK: true, Question: "q1", LatencyMS: 200, TotalTokens: 20},
		{Temperature: 0, Run: 3, OK: false, Error: "boom", LatencyMS: 300},
		{Temperature: 0.8, Run: 1, OK: true, Question: "q2", LatencyMS: 400, TotalTokens: 30},
	}
	got := summarize(results)
	if len(got) != 2 {
		t.Fatalf("summaries = %d, want 2", len(got))
	}
	if got[0].Temperature != 0 || got[0].OK != 2 || got[0].Errors != 1 || got[0].Unique != 1 {
		t.Fatalf("summary[0] = %+v", got[0])
	}
	if got[0].AvgLatencyMS != 200 || got[0].AvgTokens != 10 {
		t.Fatalf("summary[0] averages = %+v", got[0])
	}
	if got[1].Temperature != 0.8 || got[1].OK != 1 || got[1].Unique != 1 {
		t.Fatalf("summary[1] = %+v", got[1])
	}
}
