package chat

import (
	"errors"
	"strings"
	"time"

	"activity-descriptor/internal/domain"
)

// InterviewStatus is the lifecycle of one respondent's interview inside a
// research run. A run keeps at most one active interview at a time; finished
// interviews stay in the run as history.
type InterviewStatus string

const (
	InterviewActive    InterviewStatus = "active"
	InterviewCompleted InterviewStatus = "completed"
	InterviewFailed    InterviewStatus = "failed"
)

// Interview is one respondent's interview inside a research run. Several
// interviews can live in the same run, which is why the dialog history is
// attributed to an interview instead of the run alone.
type Interview struct {
	ID             string          `json:"id"`
	RunID          string          `json:"run_id"`
	RespondentName string          `json:"respondent_name,omitempty"`
	RespondentRole string          `json:"respondent_role,omitempty"`
	RespondentRef  string          `json:"respondent_ref,omitempty"`
	Status         InterviewStatus `json:"status"`
	StartedAt      time.Time       `json:"started_at"`
	FinishedAt     *time.Time      `json:"finished_at,omitempty"`
}

// MaxRespondentFieldLen caps a respondent field. The interview transcript is
// the sensitive part of the record; the respondent descriptor is deliberately
// small and anonymisable (a nickname or a role is enough).
const MaxRespondentFieldLen = 120

// ErrInvalidRespondent reports a respondent descriptor that cannot be stored.
var ErrInvalidRespondent = errors.New("chat: invalid respondent")

// NormalizeRespondent trims the respondent descriptor taken from a request. It
// does not invent values: an anonymous interview keeps empty fields.
func NormalizeRespondent(name, role, ref string) domain.Respondent {
	return domain.Respondent{
		ID:   strings.TrimSpace(ref),
		Name: strings.TrimSpace(name),
		Role: strings.TrimSpace(role),
	}
}

// ValidateRespondent rejects an oversized descriptor instead of truncating it,
// so the stored label always matches what the methodologist entered.
func ValidateRespondent(respondent domain.Respondent) error {
	for _, field := range []string{respondent.Name, respondent.Role, respondent.ID} {
		if len([]rune(field)) > MaxRespondentFieldLen {
			return ErrInvalidRespondent
		}
	}
	return nil
}
