package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Ollama строит векторы для слоя поиска; адрес и модель задаются приложением.
type Ollama struct {
	BaseURL string
	Model   string
	Client  *http.Client
}

func (o Ollama) Embed(ctx context.Context, text string) ([]float32, error) {
	base, err := url.Parse(o.BaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil {
		return nil, errors.New("неверный адрес Ollama")
	}
	if strings.TrimSpace(o.Model) == "" || strings.TrimSpace(text) == "" {
		return nil, errors.New("нужны модель и текст")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/api/embed"
	base.RawQuery = ""
	base.Fragment = ""
	data, err := json.Marshal(map[string]any{"model": o.Model, "input": text, "truncate": false})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("запрос Ollama: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Ollama вернула HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 2<<20+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 2<<20 {
		return nil, errors.New("ответ Ollama слишком большой")
	}
	var response struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err = json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("ответ Ollama: %w", err)
	}
	if len(response.Embeddings) != 1 || len(response.Embeddings[0]) != 768 {
		return nil, errors.New("Ollama должна вернуть один вектор размерности 768")
	}
	nonzero := false
	for _, v := range response.Embeddings[0] {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, errors.New("вектор содержит недопустимое число")
		}
		if v != 0 {
			nonzero = true
		}
	}
	if !nonzero {
		return nil, errors.New("Ollama вернула нулевой вектор")
	}
	return response.Embeddings[0], nil
}
