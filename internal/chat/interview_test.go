package chat

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeRespondentTrimsEveryField(t *testing.T) {
	got := NormalizeRespondent("  Респондент А ", "  аналитик ", "  hh-42  ")
	if got.Name != "Респондент А" || got.Role != "аналитик" || got.ID != "hh-42" {
		t.Fatalf("не обрезаны пробелы: %+v", got)
	}
}

func TestNormalizeRespondentAllowsAnonymousInterview(t *testing.T) {
	got := NormalizeRespondent("", "", "")
	if got.Name != "" || got.Role != "" || got.ID != "" {
		t.Fatalf("анонимное интервью должно остаться пустым: %+v", got)
	}
	if err := ValidateRespondent(got); err != nil {
		t.Fatalf("анонимное интервью не должно отклоняться: %v", err)
	}
}

func TestValidateRespondentRejectsOversizedField(t *testing.T) {
	long := strings.Repeat("я", MaxRespondentFieldLen+1)
	err := ValidateRespondent(NormalizeRespondent(long, "", ""))
	if !errors.Is(err, ErrInvalidRespondent) {
		t.Fatalf("ожидалась ErrInvalidRespondent, получено %v", err)
	}
}

func TestValidateRespondentAcceptsLimit(t *testing.T) {
	limit := strings.Repeat("я", MaxRespondentFieldLen)
	if err := ValidateRespondent(NormalizeRespondent(limit, limit, limit)); err != nil {
		t.Fatalf("поле ровно по лимиту должно приниматься: %v", err)
	}
}
