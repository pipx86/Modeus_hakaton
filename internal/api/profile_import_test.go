package api

import "testing"

func TestResumeID(t *testing.T) {
	tests := map[string]string{
		"https://hh.ru/resume/0123456789abcdef":   "0123456789abcdef",
		"https://hh.ru/resume/abc123?from=shared": "abc123",
		"abc123456":                            "abc123456",
		"https://example.com/resume/abc123456": "",
		"":                                     "",
	}
	for input, want := range tests {
		if got := resumeID(input); got != want {
			t.Fatalf("resumeID(%q) = %q, want %q", input, got, want)
		}
	}
}
