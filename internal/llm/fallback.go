package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// FallbackAttempt is one model client in an ordered fallback chain. Model is a
// human-readable label used only for logs and observability: it must never
// contain a credential or prompt text.
type FallbackAttempt struct {
	Model  string
	Client ChatClient
}

// Typed fallback errors. They are safe to log and never contain prompt text,
// request bodies or credentials.
var (
	// ErrNoFallbackAttempts marks a fallback client built without any client.
	ErrNoFallbackAttempts = errors.New("llm: fallback client requires at least one attempt")
	// ErrEmptyResponse marks a provider call that returned no content.
	ErrEmptyResponse = errors.New("llm: empty response")
)

// FallbackPolicy reports whether a failed attempt may be retried on the next
// model in the chain. ctx is the caller's context, not a per-call timeout.
type FallbackPolicy func(ctx context.Context, err error) bool

// AnswerValidator reports whether a successful provider answer satisfies the
// caller's output contract (for example the agent JSON schema).
type AnswerValidator func(answer string) error

// FallbackOption customises FallbackClient.
type FallbackOption func(*FallbackClient)

// WithFallbackLogger replaces the logger used for fallback events. The default
// is slog.Default(). Logs contain model names and error categories only.
func WithFallbackLogger(logger *slog.Logger) FallbackOption {
	return func(c *FallbackClient) {
		if logger != nil {
			c.logger = logger
		}
	}
}

// WithFallbackPolicy replaces the fallback policy. A nil policy is ignored.
func WithFallbackPolicy(policy FallbackPolicy) FallbackOption {
	return func(c *FallbackClient) {
		if policy != nil {
			c.policy = policy
		}
	}
}

// WithAnswerValidator makes the chain prefer answers that satisfy the caller's
// output contract. The validator is advisory: it never converts a provider
// response into an error, it only decides whether an unused model is worth
// trying. When every attempt returned content but none validated, the last
// answer is returned unchanged so the caller's repair/validation layer, which
// has the full request context, stays authoritative. A transport error from the
// final attempt is still returned as an error and never replaced by an earlier
// invalid answer.
func WithAnswerValidator(validate AnswerValidator) FallbackOption {
	return func(c *FallbackClient) {
		if validate != nil {
			c.validate = validate
		}
	}
}

// FallbackClient wraps an ordered chain of model clients and moves to the next
// model only when the failure is transient (timeout, unavailable, rate limit,
// empty/invalid provider response or a degraded call). Permanent failures
// (auth, invalid request, configuration, guardrail rejection, cancellation) are
// returned immediately: a second model cannot fix them and hiding them would
// make demo failures undebuggable.
type FallbackClient struct {
	attempts []FallbackAttempt
	policy   FallbackPolicy
	validate AnswerValidator
	logger   *slog.Logger
}

// NewFallbackClient builds a fallback chain from attempts, tried in order.
// Every attempt needs a client; an empty chain is rejected.
func NewFallbackClient(attempts []FallbackAttempt, opts ...FallbackOption) (*FallbackClient, error) {
	if len(attempts) == 0 {
		return nil, ErrNoFallbackAttempts
	}
	cleaned := make([]FallbackAttempt, len(attempts))
	copy(cleaned, attempts)
	for i, attempt := range cleaned {
		if attempt.Client == nil {
			return nil, fmt.Errorf("llm: fallback attempt %d has no client", i+1)
		}
	}
	client := &FallbackClient{
		attempts: cleaned,
		policy:   DefaultFallbackPolicy,
		logger:   slog.Default(),
	}
	for _, opt := range opts {
		opt(client)
	}
	return client, nil
}

// Chat implements ChatClient. It returns the first answer that passes the
// optional validator, or the first answer when no validator is configured.
// The returned error is the last provider error, unwrapped, so existing
// classification (DegradedError, Context, DomainError, GuardError) still works.
func (c *FallbackClient) Chat(ctx context.Context, messages []Message) (string, error) {
	if c == nil || len(c.attempts) == 0 {
		return "", ErrNoFallbackAttempts
	}
	if ctx == nil {
		return "", ErrNilContext
	}

	fallbackReason := ""
	for index, attempt := range c.attempts {
		if index > 0 {
			c.logger.Warn("llm: trying fallback model",
				"model", attempt.Model,
				"attempt", index+1,
				"reason", fallbackReason,
			)
		}

		answer, err := attempt.Client.Chat(ctx, messages)
		if err == nil && strings.TrimSpace(answer) == "" {
			err = ErrEmptyResponse
		}
		if err != nil {
			last := index == len(c.attempts)-1
			if last {
				c.logger.Error("llm: fallback chain exhausted",
					"model", attempt.Model,
					"attempt", index+1,
					"error_code", Code(err),
				)
				return "", err
			}
			if ctx.Err() != nil || !c.policy(ctx, err) {
				c.logger.Warn("llm: permanent error; fallback skipped",
					"model", attempt.Model,
					"attempt", index+1,
					"error_code", Code(err),
				)
				return "", err
			}
			fallbackReason = Code(err)
			continue
		}

		if c.validate != nil {
			if validationErr := c.validate(answer); validationErr != nil {
				// The validation error can embed the model answer, which may
				// contain transcript text or PII. Log only the event.
				c.logger.Warn("llm: answer failed output validation",
					"model", attempt.Model,
					"attempt", index+1,
				)
				if index == len(c.attempts)-1 {
					// No model produced a valid answer. Return the content so
					// the repair layer can retry with an explicit repair prompt
					// and report the terminal failure itself.
					return answer, nil
				}
				fallbackReason = "schema_invalid"
				continue
			}
		}

		if index > 0 {
			c.logger.Info("llm: fallback model answered",
				"model", attempt.Model,
				"attempt", index+1,
			)
		}
		return answer, nil
	}
	return "", ErrNoFallbackAttempts
}

// DefaultFallbackPolicy is the conservative production policy: move to the
// next model only for failures another model can plausibly survive.
//
// Fallback-eligible: retryable provider statuses (429/5xx/timeout), timeouts
// (including a circuit-breaker per-call timeout), unavailable providers
// (including an open circuit breaker), and 2xx responses whose body is empty or
// malformed (status 0). A 4xx invalid request is never retried on another
// model: the same request fails everywhere and hiding it would make demo
// failures undebuggable.
//
// Fail fast (no fallback): authentication, invalid request/4xx, configuration,
// guardrail rejections (input blocked or output leak), cancellations and
// caller context expiration. These are permanent or deliberate: trying another
// model would only delay the honest error.
func DefaultFallbackPolicy(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	if errors.Is(err, ErrEmptyResponse) {
		return true
	}
	if IsRetryable(err) {
		return true
	}
	switch Classify(err) {
	case ErrorTimeout, ErrorUnavailable, ErrorRateLimit:
		return true
	case ErrorInvalidResponse:
		return isUndecodableProviderResponse(err)
	default:
		return false
	}
}

// isUndecodableProviderResponse reports whether err is a 2xx provider response
// that could not be decoded into an answer (empty content, malformed JSON).
// Those deserve one attempt on another model. An upstream 4xx carries an HTTP
// status and is not retried.
func isUndecodableProviderResponse(err error) bool {
	var classified Classified
	if !errors.As(err, &classified) {
		return true
	}
	status := classified.HTTPStatus()
	return status == 0 || (status >= 200 && status < 300)
}
