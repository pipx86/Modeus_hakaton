package ingestion

import (
	"strings"
	"testing"
)

func TestNormalizeProgramKeyValid(t *testing.T) {
	cases := []string{
		"mirea/09.03.01/2024/full-time",
		"ab/cd/2000/other",
		"vgu/01.02/2100/part-time",
		"org-1/python.3/2026/evening",
		"a1/b2/2030/distance",
		"mgtu/09.03.01/2025/mixed",
	}
	for _, raw := range cases {
		got, err := NormalizeProgramKey(raw)
		if err != nil {
			t.Errorf("NormalizeProgramKey(%q) вернул ошибку: %v", raw, err)
			continue
		}
		if got != raw {
			t.Errorf("NormalizeProgramKey(%q) = %q, ожидали исходный ключ", raw, got)
		}
		if !IsCanonicalProgramKey(raw) {
			t.Errorf("IsCanonicalProgramKey(%q) = false", raw)
		}
	}
}

func TestNormalizeProgramKeyInvalid(t *testing.T) {
	cases := map[string]string{
		"":                                "пуст",
		"   ":                             "пуст",
		"mirea":                           "формат",
		"mirea/09.03.01/2024":             "формат",
		"mirea/09.03.01/2024/full-time/x": "формат",
		"MIREA/09.03.01/2024/full-time":   "org",
		"mirea/09.03.01/2024/FULL-TIME":   "form",
		"mirea/09.03.01/2024/full_time":   "form",
		"m/09.03.01/2024/full-time":       "org",
		"mirea/0/2024/full-time":          "code",
		"mirea/09_03_01/2024/full-time":   "code",
		"mirea/09.03.01/1999/full-time":   "year",
		"mirea/09.03.01/2101/full-time":   "year",
		"mirea/09.03.01/24/full-time":     "year",
		"mirea/09.03.01/2024/":            "form",
	}
	for raw, fragment := range cases {
		if _, err := NormalizeProgramKey(raw); err == nil {
			t.Errorf("NormalizeProgramKey(%q) не вернул ошибку", raw)
		} else if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(fragment)) {
			t.Errorf("NormalizeProgramKey(%q): ошибка %q не содержит %q", raw, err, fragment)
		}
	}
}

func TestLegacyProgramKeyWarnings(t *testing.T) {
	cases := []string{"prog-key", "program_2024", "old/program/key"}
	for _, raw := range cases {
		warning, ok := ProgramKeyWarning(raw)
		if !ok || warning == "" {
			t.Errorf("ProgramKeyWarning(%q) = %q, %v; ожидали предупреждение", raw, warning, ok)
			continue
		}
		key, gotWarning, valid := NormalizeProgramKeyLenient(raw)
		if !valid || key != raw || gotWarning == "" {
			t.Errorf("NormalizeProgramKeyLenient(%q) = %q, %q, %v", raw, key, gotWarning, valid)
		}
	}
	if warning, ok := ProgramKeyWarning("mirea/09.03.01/2024/full-time"); ok || warning != "" {
		t.Errorf("канонический ключ не должен давать предупреждение: %q", warning)
	}
	if _, _, ok := NormalizeProgramKeyLenient("  "); ok {
		t.Error("пустой ключ не должен считаться валидным")
	}
	if key, warning, ok := NormalizeProgramKeyLenient("  mirea/09.03.01/2024/full-time  "); !ok || key != "mirea/09.03.01/2024/full-time" || warning != "" {
		t.Errorf("NormalizeProgramKeyLenient с пробелами = %q, %q, %v", key, warning, ok)
	}
}

func TestNextProgramVersion(t *testing.T) {
	cases := []struct {
		current int
		exists  bool
		want    int
	}{
		{0, false, 1},
		{0, true, 1},
		{1, true, 2},
		{2, true, 3},
		{7, false, 1},
		{-1, true, 1},
	}
	for _, tc := range cases {
		if got := NextProgramVersion(tc.current, tc.exists); got != tc.want {
			t.Errorf("NextProgramVersion(%d, %v) = %d, ожидали %d", tc.current, tc.exists, got, tc.want)
		}
	}
}
