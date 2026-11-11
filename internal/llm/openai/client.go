package openai

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
	chatPath        = "/chat/completions"
	maxErrorBody    = 512
	maxResponseBody = 1 << 20
)

// Client is an OpenAI-compatible chat client (OpenRouter, Groq, Together, ...).
type Client struct {
	baseURL         string
	apiKey          string
	model           string
	referer         string
	title           string
	maxTokens       int
	temperature     *float64
	reasoningEffort string
	responseFormat  json.RawMessage
	http            *http.Client
	logger          *slog.Logger
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

// WithLogger replaces the default logger.
func WithLogger(logger *slog.Logger) Option {
	return func(c *Client) {
		if logger != nil {
			c.logger = logger
		}
	}
}

// WithMaxTokens sets the maximum number of generated tokens.
func WithMaxTokens(maxTokens int) Option {
	return func(c *Client) {
		if maxTokens > 0 {
			c.maxTokens = maxTokens
		}
	}
}

// WithTemperature sets the sampling temperature.
func WithTemperature(temperature float64) Option {
	return func(c *Client) {
		if temperature >= 0 {
			c.temperature = &temperature
		}
	}
}

// WithReasoningEffort sets low/medium/high reasoning effort.
func WithReasoningEffort(effort string) Option {
	return func(c *Client) {
		if effort != "" {
			c.reasoningEffort = effort
		}
	}
}

// WithResponseFormat sets the OpenAI-compatible response_format object.
func WithResponseFormat(format json.RawMessage) Option {
	return func(c *Client) {
		if len(format) > 0 {
			c.responseFormat = format
		}
	}
}

// New creates a client. APIKey and Model are required.
func New(cfg Config, opts ...Option) (*Client, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.APIKey == "" {
		return nil, errors.New("openai: api key is required")
	}
	if cfg.Model == "" {
		return nil, errors.New("openai: model is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.Title == "" {
		cfg.Title = DefaultTitle
	}
	if cfg.ReasoningEffort != "" {
		cfg.ReasoningEffort = strings.ToLower(cfg.ReasoningEffort)
		if !validReasoningEffort(cfg.ReasoningEffort) {
			return nil, fmt.Errorf("openai: reasoning effort must be low, medium or high, got %q", cfg.ReasoningEffort)
		}
	}
	client := &Client{
		baseURL:         strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:          cfg.APIKey,
		model:           cfg.Model,
		referer:         cfg.HTTPReferer,
		title:           cfg.Title,
		maxTokens:       cfg.MaxTokens,
		temperature:     cfg.Temperature,
		reasoningEffort: cfg.ReasoningEffort,
		responseFormat:  cfg.ResponseFormat,
		http:            &http.Client{Timeout: cfg.Timeout},
		logger:          slog.Default(),
	}
	for _, opt := range opts {
		opt(client)
	}
	return client, nil
}

type chatRequest struct {
	Model           string          `json:"model"`
	Messages        []llm.Message   `json:"messages"`
	Stream          bool            `json:"stream"`
	MaxTokens       int             `json:"max_tokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	ResponseFormat  json.RawMessage `json:"response_format,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error,omitempty"`
}

// Chat sends messages and returns the assistant content.
func (c *Client) Chat(ctx context.Context, messages []llm.Message) (string, error) {
	if ctx == nil {
		return "", errors.New("openai: nil context")
	}
	if len(messages) == 0 {
		return "", errors.New("openai: at least one message is required")
	}

	payload, err := json.Marshal(chatRequest{
		Model:           c.model,
		Messages:        messages,
		Stream:          false,
		MaxTokens:       c.maxTokens,
		Temperature:     c.temperature,
		ReasoningEffort: c.reasoningEffort,
		ResponseFormat:  c.responseFormat,
	})
	if err != nil {
		return "", fmt.Errorf("openai: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+chatPath, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("openai: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if c.referer != "" {
		req.Header.Set("HTTP-Referer", c.referer)
	}
	if c.title != "" {
		req.Header.Set("X-Title", c.title)
	}

	start := time.Now()
	resp, err := c.http.Do(req)
	duration := time.Since(start)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			c.logFailure(ctx, "request cancelled", 0, duration, ctxErr)
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				return "", fmt.Errorf("%w: %v", ErrTimeout, ctxErr)
			}
			return "", fmt.Errorf("openai: chat cancelled: %w", ctxErr)
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			c.logFailure(ctx, "request timeout", 0, duration, err)
			return "", fmt.Errorf("%w: %v", ErrTimeout, err)
		}
		c.logFailure(ctx, "request failed", 0, duration, err)
		return "", fmt.Errorf("openai: chat request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		upstream := &UpstreamError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
		c.logFailure(ctx, "upstream error", resp.StatusCode, duration, upstream)
		return "", upstream
	}

	var decoded chatResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody))
	if err := decoder.Decode(&decoded); err != nil {
		elapsed := time.Since(start)
		if ctxErr := ctx.Err(); ctxErr != nil {
			c.logFailure(ctx, "decode timeout", resp.StatusCode, elapsed, ctxErr)
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				return "", fmt.Errorf("%w: %v", ErrTimeout, ctxErr)
			}
			return "", fmt.Errorf("openai: chat cancelled: %w", ctxErr)
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			c.logFailure(ctx, "decode timeout", resp.StatusCode, elapsed, err)
			return "", fmt.Errorf("%w: %v", ErrTimeout, err)
		}
		c.logFailure(ctx, "decode error", resp.StatusCode, elapsed, err)
		return "", fmt.Errorf("%w: %v", ErrDecode, err)
	}
	if decoded.Error != nil {
		upstream := &UpstreamError{StatusCode: resp.StatusCode, Body: decoded.Error.Message}
		c.logFailure(ctx, "upstream error", resp.StatusCode, time.Since(start), upstream)
		return "", upstream
	}
	if len(decoded.Choices) == 0 || decoded.Choices[0].Message.Content == "" {
		err := errors.New("empty response content")
		c.logFailure(ctx, "empty response", resp.StatusCode, time.Since(start), err)
		return "", fmt.Errorf("%w: %v", ErrDecode, err)
	}
	return decoded.Choices[0].Message.Content, nil
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
	c.logger.ErrorContext(ctx, "openai chat failed: "+message, attrs...)
}

func validReasoningEffort(effort string) bool {
	switch effort {
	case "low", "medium", "high":
		return true
	default:
		return false
	}
}
