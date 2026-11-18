package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultHHTokenURL is the application-token endpoint documented by hh.ru.
	DefaultHHTokenURL = "https://hh.ru/oauth/token"
	// hhTokenResponseLimit bounds a token response; it must never be printed.
	hhTokenResponseLimit = 1 << 20
)

// HHToken is the application access token returned by hh.ru.
//
// AccessToken is a secret. Callers must not log it, store it in the database,
// commit it to Git, or include it in error messages.
type HHToken struct {
	AccessToken string
	TokenType   string
	ExpiresIn   int
}

// FetchHHApplicationToken exchanges hh.ru application credentials for an
// application access token (grant_type=client_credentials).
//
// The request is server-to-server. The returned token is short-lived and is
// intended to be passed to FetchHH as a Bearer token.
func FetchHHApplicationToken(ctx context.Context, client *http.Client, endpoint, clientID, clientSecret, userAgent string) (HHToken, error) {
	if ctx == nil {
		return HHToken{}, errors.New("hh: nil context")
	}
	clientID = strings.TrimSpace(clientID)
	clientSecret = strings.TrimSpace(clientSecret)
	userAgent = strings.TrimSpace(userAgent)
	if clientID == "" || clientSecret == "" {
		return HHToken{}, errors.New("hh: client id and client secret are required")
	}
	if endpoint == "" {
		endpoint = DefaultHHTokenURL
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return HHToken{}, errors.New("hh: invalid token endpoint")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return HHToken{}, fmt.Errorf("hh: create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if userAgent != "" {
		req.Header.Set("HH-User-Agent", userAgent)
		req.Header.Set("User-Agent", userAgent)
	}
	res, err := client.Do(req)
	if err != nil {
		return HHToken{}, fmt.Errorf("hh: token request: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, hhTokenResponseLimit+1))
	if err != nil {
		return HHToken{}, fmt.Errorf("hh: read token response: %w", err)
	}
	if len(data) > hhTokenResponseLimit {
		return HHToken{}, errors.New("hh: token response is too large")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return HHToken{}, fmt.Errorf("hh: token endpoint returned HTTP %d", res.StatusCode)
	}

	var payload struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return HHToken{}, errors.New("hh: invalid token response")
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return HHToken{}, errors.New("hh: token response has no access_token")
	}
	return HHToken{
		AccessToken: payload.AccessToken,
		TokenType:   payload.TokenType,
		ExpiresIn:   payload.ExpiresIn,
	}, nil
}
