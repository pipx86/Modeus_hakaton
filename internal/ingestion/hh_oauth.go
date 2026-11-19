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
	"sync"
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

// HHTokenSource выдаёт действующий app-token hh.ru и обновляет его автоматически.
//
// Порядок выбора:
//  1. статический HH_ACCESS_TOKEN (если задан) — без обновления;
//  2. иначе client_credentials по HH_CLIENT_ID / HH_CLIENT_SECRET.
//
// Токен кэшируется в памяти. Обновление запускается, когда до истечения
// осталось меньше refreshMargin (по умолчанию 60 с). Ответ
// "app token refresh too early" не затирает ещё валидный кэш.
type HHTokenSource struct {
	Client       *http.Client
	Endpoint     string
	ClientID     string
	ClientSecret string
	UserAgent    string
	// StaticToken, если не пустой, используется как есть и никогда не обновляется.
	StaticToken string

	mu            sync.Mutex
	token         string
	expiresAt     time.Time
	refreshMargin time.Duration
}

// NewHHTokenSource собирает источник токена из переменных окружения и опций.
// StaticToken (HH_ACCESS_TOKEN) имеет приоритет над client credentials.
func NewHHTokenSource(client *http.Client, endpoint, clientID, clientSecret, userAgent, staticToken string) *HHTokenSource {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if endpoint == "" {
		endpoint = DefaultHHTokenURL
	}
	return &HHTokenSource{
		Client:        client,
		Endpoint:      endpoint,
		ClientID:      strings.TrimSpace(clientID),
		ClientSecret:  strings.TrimSpace(clientSecret),
		UserAgent:     strings.TrimSpace(userAgent),
		StaticToken:   strings.TrimSpace(staticToken),
		refreshMargin: 60 * time.Second,
	}
}

// Token возвращает Bearer-токен. При необходимости выполняет client_credentials
// и обновляет кэш. Пустая строка означает «работать без токена» (публичный поиск).
func (s *HHTokenSource) Token(ctx context.Context) (string, error) {
	if s == nil {
		return "", nil
	}
	if s.StaticToken != "" {
		return s.StaticToken, nil
	}
	if s.ClientID == "" || s.ClientSecret == "" {
		return "", nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if s.token != "" && now.Add(s.refreshMargin).Before(s.expiresAt) {
		return s.token, nil
	}

	tok, err := FetchHHApplicationToken(ctx, s.Client, s.Endpoint, s.ClientID, s.ClientSecret, s.UserAgent)
	if err != nil {
		// hh.ru часто отвечает 403 "app token refresh too early".
		// Если у нас ещё есть непросроченный кэш — продолжаем с ним.
		if s.token != "" && now.Before(s.expiresAt) {
			return s.token, nil
		}
		return "", err
	}

	s.token = tok.AccessToken
	expiresIn := tok.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600 // разумный fallback, если API не вернул expires_in
	}
	s.expiresAt = now.Add(time.Duration(expiresIn) * time.Second)
	return s.token, nil
}
