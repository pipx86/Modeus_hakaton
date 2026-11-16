package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"activity-descriptor/internal/llm"
)

const (
	chatPath        = "/api/chat"
	maxErrorBody    = 512
	maxResponseBody = 1 << 20
)

// Client is an HTTP client for Ollama's /api/chat endpoint.
type Client struct {
	baseURL       string
	model         string
	http          *http.Client
	logger        *slog.Logger
	usageObserver llm.UsageObserver
}

// Option customises Client.
type Option func(*Client)

// WithHTTPClient replaces the default HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.http = client
		}
	}
}

// WithUsageObserver receives token usage for every successful call.
func WithUsageObserver(observer llm.UsageObserver) Option {
	return func(c *Client) {
		c.usageObserver = observer
	}
}

// WithLogger replaces the default logger.
func WithLogger(logger *slog.Logger) Option {
	return func(c *Client) {
		if logger != nil {
			c.logger = logger
		}
	}
}

// New creates a client. Model is required; BaseURL and Timeout have defaults.
func New(cfg Config, opts ...Option) (*Client, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Model == "" {
		return nil, errors.New("ollama: model is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	client := &Client{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		model:   cfg.Model,
		http:    &http.Client{Timeout: cfg.Timeout},
		logger:  slog.Default(),
	}
	for _, opt := range opts {
		opt(client)
	}
	return client, nil
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []llm.Message `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatResponse struct {
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	Done            bool   `json:"done"`
	Error           string `json:"error"`
}

// Chat sends messages to Ollama and returns the assistant content.
// A non-2xx response, timeout or malformed payload is returned as an error;
// the process is not terminated.
func (c *Client) Chat(ctx context.Context, messages []llm.Message) (string, error) {
	if ctx == nil {
		return "", errors.New("ollama: nil context")
	}
	if len(messages) == 0 {
		return "", errors.New("ollama: at least one message is required")
	}

	payload, err := json.Marshal(chatRequest{Model: c.model, Messages: messages, Stream: false})
	if err != nil {
		return "", fmt.Errorf("ollama: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+chatPath, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("ollama: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := c.http.Do(req)
	duration := time.Since(start)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			c.logFailure(ctx, "request cancelled", 0, duration, ctxErr)
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				return "", llm.NewDomainError(llm.ErrorTimeout, "ollama", 0, true, fmt.Errorf("%w: %v", ErrTimeout, ctxErr))
			}
			return "", llm.NewDomainError(llm.ErrorCanceled, "ollama", 0, false, fmt.Errorf("ollama: chat cancelled: %w", ctxErr))
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			c.logFailure(ctx, "request timeout", 0, duration, err)
			return "", llm.NewDomainError(llm.ErrorTimeout, "ollama", 0, true, fmt.Errorf("%w: %v", ErrTimeout, err))
		}
		c.logFailure(ctx, "request failed", 0, duration, err)
		return "", llm.NewDomainError(llm.ErrorUnavailable, "ollama", 0, true, fmt.Errorf("ollama: chat request: %w", err))
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		upstream := &UpstreamError{
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(body)),
		}
		c.logFailure(ctx, "upstream error", resp.StatusCode, duration, upstream)
		return "", llm.NewDomainError(llm.KindForStatus(resp.StatusCode), "ollama", resp.StatusCode, llm.RetryableStatus(resp.StatusCode), upstream)
	}

	var decoded chatResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody))
	if err := decoder.Decode(&decoded); err != nil {
		elapsed := time.Since(start)
		if ctxErr := ctx.Err(); ctxErr != nil {
			c.logFailure(ctx, "decode timeout", resp.StatusCode, elapsed, ctxErr)
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				return "", llm.NewDomainError(llm.ErrorTimeout, "ollama", 0, true, fmt.Errorf("%w: %v", ErrTimeout, ctxErr))
			}
			return "", llm.NewDomainError(llm.ErrorCanceled, "ollama", 0, false, fmt.Errorf("ollama: chat cancelled: %w", ctxErr))
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			c.logFailure(ctx, "decode timeout", resp.StatusCode, elapsed, err)
			return "", llm.NewDomainError(llm.ErrorTimeout, "ollama", 0, true, fmt.Errorf("%w: %v", ErrTimeout, err))
		}
		c.logFailure(ctx, "decode error", resp.StatusCode, elapsed, err)
		return "", llm.NewDomainError(llm.ErrorInvalidResponse, "ollama", 0, false, fmt.Errorf("%w: %v", ErrDecode, err))
	}
	if decoded.Error != "" {
		upstream := &UpstreamError{StatusCode: resp.StatusCode, Body: decoded.Error}
		c.logFailure(ctx, "upstream error", resp.StatusCode, time.Since(start), upstream)
		return "", llm.NewDomainError(llm.KindForStatus(resp.StatusCode), "ollama", resp.StatusCode, llm.RetryableStatus(resp.StatusCode), upstream)
	}
	if decoded.Message.Content == "" {
		err := errors.New("empty response content")
		c.logFailure(ctx, "empty response", resp.StatusCode, time.Since(start), err)
		return "", llm.NewDomainError(llm.ErrorInvalidResponse, "ollama", 0, false, fmt.Errorf("%w: %v", ErrDecode, err))
	}
	if c.usageObserver != nil {
		c.usageObserver(llm.Usage{
			PromptTokens:     decoded.PromptEvalCount,
			CompletionTokens: decoded.EvalCount,
			TotalTokens:      decoded.PromptEvalCount + decoded.EvalCount,
			Model:            c.model,
		})
	}
	return decoded.Message.Content, nil
}

func (c *Client) logFailure(ctx context.Context, message string, status int, duration time.Duration, err error) {
	attrs := []any{
		"model", c.model,
		"url", c.baseURL + chatPath,
		"duration_ms", duration.Milliseconds(),
		"error", err,
	}
	if status != 0 {
		attrs = append(attrs, "status", status)
	}
	c.logger.ErrorContext(ctx, "ollama chat failed: "+message, attrs...)
}
