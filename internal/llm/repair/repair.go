// Package repair retries invalid LLM responses with an explicit repair prompt
// and reports a failed run status after the configured number of repairs.
package repair

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

// DefaultMaxRepairs is the number of repair attempts after the initial answer.
const DefaultMaxRepairs = 2

// ErrInvalidResponse marks responses that failed validation.
var ErrInvalidResponse = errors.New("llm: invalid response")

// Validator checks one model answer. A nil error means the answer is valid.
type Validator func(answer string) error

// Error is a terminal LLM failure with an explicit run status.
type Error struct {
	Status   domain.RunStatus
	Attempts int
	Err      error
}

func (e *Error) Error() string {
	if e == nil {
		return "llm: repair error"
	}
	return fmt.Sprintf("llm: run %s after %d attempt(s): %v", e.Status, e.Attempts, e.Err)
}

// Unwrap allows errors.Is/errors.As to reach the underlying cause.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Client wraps a ChatClient and repairs invalid answers up to maxRepairs times.
type Client struct {
	next       llm.ChatClient
	validate   Validator
	maxRepairs int
}

// Option customises Client.
type Option func(*Client)

// WithMaxRepairs sets the number of repair attempts after the initial answer.
// Negative values are ignored.
func WithMaxRepairs(maxRepairs int) Option {
	return func(c *Client) {
		if maxRepairs >= 0 {
			c.maxRepairs = maxRepairs
		}
	}
}

// New creates a repairing client. Both next and validate are required.
func New(next llm.ChatClient, validate Validator, opts ...Option) (*Client, error) {
	if next == nil {
		return nil, errors.New("repair: next client is required")
	}
	if validate == nil {
		return nil, errors.New("repair: validator is required")
	}
	client := &Client{
		next:       next,
		validate:   validate,
		maxRepairs: DefaultMaxRepairs,
	}
	for _, opt := range opts {
		opt(client)
	}
	return client, nil
}

// Chat returns a valid answer or a terminal *Error with RunFailed status.
// An invalid answer never panics and never escapes as a bare string.
func (c *Client) Chat(ctx context.Context, messages []llm.Message) (string, error) {
	if ctx == nil {
		return "", &Error{
			Status: domain.RunFailed,
			Err:    errors.New("repair: nil context"),
		}
	}

	history := append([]llm.Message(nil), messages...)
	attempts := 0

	for {
		attempts++
		answer, err := c.next.Chat(ctx, history)
		if err != nil {
			return "", &Error{
				Status:   domain.RunFailed,
				Attempts: attempts,
				Err:      err,
			}
		}

		validationErr := c.validate(answer)
		if validationErr == nil {
			return answer, nil
		}

		invalid := fmt.Errorf("%w: %v", ErrInvalidResponse, validationErr)
		if attempts > c.maxRepairs {
			return "", &Error{
				Status:   domain.RunFailed,
				Attempts: attempts,
				Err:      invalid,
			}
		}

		history = append(history,
			llm.Message{Role: llm.RoleAssistant, Content: answer},
			llm.Message{Role: llm.RoleUser, Content: repairPrompt(validationErr)},
		)
	}
}

func repairPrompt(validationErr error) string {
	message := strings.TrimSpace(validationErr.Error())
	if message == "" {
		message = "response did not match the schema"
	}
	return "Предыдущий ответ не прошёл валидацию: " + message +
		". Исправь ответ. Верни только валидный JSON по схеме, без markdown и пояснений."
}

// MarkRunFailed writes the terminal status from err into run and returns err.
// It is a no-op for non-terminal errors and nil runs.
func MarkRunFailed(run *domain.Run, err error) error {
	var terminal *Error
	if run != nil && errors.As(err, &terminal) {
		run.Status = terminal.Status
	}
	return err
}
