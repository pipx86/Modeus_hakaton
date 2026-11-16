// Package webhook sends signed event payloads to an HTTP endpoint.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// SignatureHeader contains the lowercase hexadecimal HMAC-SHA256 of the exact request body.
const SignatureHeader = "X-Webhook-Signature"

type Sender struct {
	endpoint string
	secret   []byte
	client   *http.Client
}

// NewSender configures the webhook endpoint and its shared secret.
func NewSender(endpoint string, secret []byte) (*Sender, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, errors.New("webhook endpoint must be an HTTP(S) URL without credentials")
	}
	if len(secret) == 0 {
		return nil, errors.New("webhook secret is required")
	}
	return &Sender{endpoint: endpoint, secret: bytes.Clone(secret), client: &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// Send posts the exact JSON bytes provided and signs those same bytes.
// A non-2xx response or a network error is returned to the caller for retry.
func (s *Sender) Send(ctx context.Context, payload []byte) error {
	if !json.Valid(payload) {
		return errors.New("webhook payload must be valid JSON")
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SignatureHeader, hex.EncodeToString(mac.Sum(nil)))
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("send webhook: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}
