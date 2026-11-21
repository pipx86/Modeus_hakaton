package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// runInfo — поля run, которые нужны CLI. Остальные поля ответа API не
// используются, поэтому структура намеренно узкая.
type runInfo struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Profession string `json:"profession"`
}

// chatMessage — сообщение интервью в контракте GET /runs/{id}/messages.
type chatMessage struct {
	ID      string `json:"id"`
	Role    string `json:"role"`
	Content string `json:"content"`
}

// treeNode — узел дерева; обоснования считаются по длине evidence.
type treeNode struct {
	ID       string            `json:"id"`
	Evidence []json.RawMessage `json:"evidence"`
}

// apiError — не-2xx ответ API с сохранённым HTTP-статусом и кодом из тела
// {code, message}. Код нужен, чтобы отличить CHAT_CONFLICT и SESSION_FINISHED
// от остальных ошибок.
type apiError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *apiError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("API вернул HTTP %d (%s): %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("API вернул HTTP %d: %s", e.StatusCode, e.Message)
}

// apiClient — минимальный HTTP-клиент CLI. Токен подставляется в каждый
// запрос; пути строятся от базового адреса без суффикса /api/v1.
type apiClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func newAPIClient(baseURL, token string, timeout time.Duration) *apiClient {
	return &apiClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: timeout},
	}
}

const apiPrefix = "/api/v1"

// runPath собирает путь к run с экранированием id: id приходит от сервера, но
// не должен ломать URL, даже если когда-нибудь перестанет быть UUID.
func runPath(runID, suffix string) string {
	return "/runs/" + url.PathEscape(runID) + suffix
}

// do выполняет JSON-запрос и разбирает ответ в out. Не-2xx ответы
// превращаются в *apiError, сетевые ошибки — в обычную ошибку с контекстом.
func (c *apiClient) do(ctx context.Context, method, path string, body, out any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("сериализовать тело запроса: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+apiPrefix+path, payload)
	if err != nil {
		return fmt.Errorf("собрать запрос %s %s: %w", method, path, err)
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("запрос %s %s: %w", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("прочитать ответ %s %s: %w", method, path, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return newAPIError(response.StatusCode, data)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("разобрать ответ %s %s: %w", method, path, err)
		}
	}
	return nil
}

// newAPIError разбирает тело ошибки контракта {code, message}; если тело не
// JSON (например, HTML от прокси), сохраняется хотя бы текст ответа.
func newAPIError(status int, data []byte) *apiError {
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	message := strings.TrimSpace(string(data))
	if err := json.Unmarshal(data, &body); err == nil {
		if strings.TrimSpace(body.Message) != "" {
			message = body.Message
		}
		return &apiError{StatusCode: status, Code: body.Code, Message: message}
	}
	if message == "" {
		message = http.StatusText(status)
	}
	return &apiError{StatusCode: status, Message: message}
}

// createRun создаёт chat-прогон: интервью начинается сразу после ответа 201.
func (c *apiClient) createRun(ctx context.Context, profession string) (runInfo, error) {
	var run runInfo
	err := c.do(ctx, http.MethodPost, "/runs", map[string]string{
		"profession": profession,
		"type":       "chat",
	}, &run)
	return run, err
}

// getRun читает текущий статус прогона.
func (c *apiClient) getRun(ctx context.Context, runID string) (runInfo, error) {
	var run runInfo
	err := c.do(ctx, http.MethodGet, runPath(runID, ""), nil, &run)
	return run, err
}

// messages возвращает историю интервью в порядке следования.
func (c *apiClient) messages(ctx context.Context, runID string) ([]chatMessage, error) {
	var history []chatMessage
	err := c.do(ctx, http.MethodGet, runPath(runID, "/messages"), nil, &history)
	return history, err
}

// postAnswer отправляет ответ респондента.
func (c *apiClient) postAnswer(ctx context.Context, runID, content string) error {
	return c.do(ctx, http.MethodPost, runPath(runID, "/messages"), map[string]string{
		"content": content,
	}, nil)
}

// tree возвращает узлы дерева. API умеет отдавать как голый массив (старый
// контракт), так и объект {nodes, insights}; поддерживаются оба варианта.
func (c *apiClient) tree(ctx context.Context, runID string) ([]treeNode, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, runPath(runID, "/tree"), nil, &raw); err != nil {
		return nil, err
	}
	var envelope struct {
		Nodes []treeNode `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Nodes != nil {
		return envelope.Nodes, nil
	}
	var nodes []treeNode
	if err := json.Unmarshal(raw, &nodes); err != nil {
		return nil, fmt.Errorf("разобрать дерево: %w", err)
	}
	return nodes, nil
}

// conflicts возвращает число разрешённых конфликтов синтезатора. Эндпоинт
// доступен только роли admin, поэтому ошибка не фатальна для CLI: счётчик
// помечается как недоступный, а прогон продолжается.
func (c *apiClient) conflicts(ctx context.Context, runID string) (int, error) {
	var items []json.RawMessage
	if err := c.do(ctx, http.MethodGet, runPath(runID, "/conflicts"), nil, &items); err != nil {
		return 0, err
	}
	return len(items), nil
}
