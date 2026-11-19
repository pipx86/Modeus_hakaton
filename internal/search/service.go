package search

import (
	"activity-descriptor/internal/embeddings"
	"activity-descriptor/internal/logging"
	"activity-descriptor/internal/store"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// DefaultOllamaURL используется, когда OLLAMA_URL не задан. Локальная
	// Ollama по умолчанию слушает именно этот адрес.
	DefaultOllamaURL = "http://127.0.0.1:11434"
	// DefaultEmbedModel используется, когда OLLAMA_EMBED_MODEL не задан.
	DefaultEmbedModel = "nomic-embed-text"
	// EmbeddingDimensions — размерность вектора; должна совпадать с
	// vector(768) в необязательной миграции и с проверкой клиента Ollama.
	EmbeddingDimensions = 768
	// fragmentRunes — целевой размер фрагмента в символах (рунах).
	fragmentRunes = 800
	// fragmentOverlap — перекрытие соседних фрагментов, чтобы цитата на
	// границе не терялась.
	fragmentOverlap = 100
)

// Ошибки IndexSource, по которым HTTP-слой и backfill выбирают безопасную
// реакцию: неверный/пустой источник и гонка с редактированием текста.
var (
	ErrSourceNotFound = errors.New("источник не найден")
	ErrEmptySource    = errors.New("источник не содержит текста")
	ErrSourceChanged  = errors.New("источник изменился во время векторизации; повторите операцию")
)

// Config — параметры эмбеддингов, общие для API-индексации, RAG-ретривера и
// backfill-команды. Значения по умолчанию совпадают с локальной Ollama.
type Config struct {
	OllamaURL  string
	EmbedModel string
}

// ConfigFromEnv читает OLLAMA_URL и OLLAMA_EMBED_MODEL. Пустые значения
// заменяются локальными умолчаниями, чтобы `go run ./cmd/server` работал на
// машине с установленной Ollama без обязательной настройки .env.
func ConfigFromEnv() Config {
	address := strings.TrimRight(strings.TrimSpace(os.Getenv("OLLAMA_URL")), "/")
	if address == "" {
		address = DefaultOllamaURL
	}
	model := strings.TrimSpace(os.Getenv("OLLAMA_EMBED_MODEL"))
	if model == "" {
		model = DefaultEmbedModel
	}
	return Config{OllamaURL: address, EmbedModel: model}
}

// Client returns the Ollama embedding client described by the config.
func (c Config) Client() embeddings.Ollama {
	return embeddings.Ollama{BaseURL: c.OllamaURL, Model: c.EmbedModel}
}

// Service builds the search service for a PostgreSQL pool.
func (c Config) Service(pool *pgxpool.Pool) Service {
	return Service{Pool: pool, Model: c.Client()}
}

// Service связывает хранилище фрагментов с моделью эмбеддингов.
type Service struct {
	Pool  *pgxpool.Pool
	Model embeddings.Ollama
}

// embed оборачивает клиента модели, чтобы сбой эмбеддинга был виден в логах:
// store при такой ошибке молча переключается на BM25, и без лога это выглядело
// бы как обычный успешный поиск.
func (s Service) embed(ctx context.Context, text string) ([]float32, error) {
	vector, err := s.Model.Embed(ctx, text)
	if err != nil && ctx.Err() == nil {
		logging.L().Warn("embedding_request_failed",
			"model", s.Model.Model,
			"base_url", s.Model.BaseURL,
			"error", err)
	}
	return vector, err
}

// IndexSource строит фрагменты источника и векторы текущей модели.
// Новые фрагменты и векторы записываются одной транзакцией после успешной
// векторизации: при сбое модели или Ollama прежний индекс остаётся на месте.
func (s Service) IndexSource(ctx context.Context, id pgtype.UUID) error {
	if s.Pool == nil {
		return errors.New("нет подключения к PostgreSQL: индексация недоступна")
	}
	if !id.Valid {
		return errors.New("нужен ID источника")
	}
	var text string
	if err := s.Pool.QueryRow(ctx, "SELECT COALESCE(content,'') FROM sources WHERE id=$1", id).Scan(&text); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSourceNotFound
		}
		return fmt.Errorf("прочитать источник: %w", err)
	}
	if strings.TrimSpace(text) == "" {
		return ErrEmptySource
	}
	started := time.Now()
	runes := []rune(text)
	quotes := make([]string, 0, len(runes)/fragmentRunes+1)
	vectors := make([]string, 0, cap(quotes))
	for start := 0; start < len(runes); {
		end := start + fragmentRunes
		if end > len(runes) {
			end = len(runes)
		}
		quote := string(runes[start:end])
		vector, err := s.embed(ctx, quote)
		if err != nil {
			return fmt.Errorf("построить вектор фрагмента %d: %w", len(quotes), err)
		}
		parts := make([]string, len(vector))
		for i, v := range vector {
			parts[i] = strconv.FormatFloat(float64(v), 'g', -1, 32)
		}
		quotes = append(quotes, quote)
		vectors = append(vectors, "["+strings.Join(parts, ",")+"]")
		if end == len(runes) {
			break
		}
		start = end - fragmentOverlap
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("начать транзакцию индексации: %w", err)
	}
	defer tx.Rollback(context.Background())
	// Источник мог измениться во время работы модели: не записываем устаревшие векторы.
	var current string
	if err = tx.QueryRow(ctx, "SELECT COALESCE(content,'') FROM sources WHERE id=$1 FOR UPDATE", id).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSourceNotFound
		}
		return fmt.Errorf("проверить источник перед записью: %w", err)
	}
	if current != text {
		return ErrSourceChanged
	}
	if _, err = tx.Exec(ctx, "DELETE FROM source_fragments WHERE source_id=$1", id); err != nil {
		return fmt.Errorf("удалить прежние фрагменты (применены ли миграции migrations_optional/vector?): %w", err)
	}
	for i, quote := range quotes {
		var fragment pgtype.UUID
		if err = tx.QueryRow(ctx, "INSERT INTO source_fragments(source_id,ordinal,quote) VALUES($1,$2,$3) RETURNING id", id, i, quote).Scan(&fragment); err != nil {
			return fmt.Errorf("сохранить фрагмент %d: %w", i, err)
		}
		if _, err = tx.Exec(ctx, "INSERT INTO fragment_embeddings(fragment_id,model,embedding) VALUES($1,$2,$3::vector)", fragment, s.Model.Model, vectors[i]); err != nil {
			return fmt.Errorf("сохранить вектор фрагмента %d (применены ли миграции migrations_optional/vector?): %w", i, err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("зафиксировать индекс источника: %w", err)
	}
	logging.L().Debug("source_indexed",
		"source_id", id.String(),
		"model", s.Model.Model,
		"fragments", len(quotes),
		"duration_ms", time.Since(started).Milliseconds())
	return nil
}

// Search использует реальный клиент модели; store переключается на BM25 при его сбое.
func (s Service) Search(ctx context.Context, run pgtype.UUID, text string, limit int) ([]store.SearchHit, string, error) {
	if s.Pool == nil {
		return nil, "", fmt.Errorf("нет подключения к PostgreSQL")
	}
	hits, mode, err := store.New(s.Pool).SearchFragments(ctx, run, text, s.Model.Model, limit, s.embed)
	if err != nil {
		return nil, mode, err
	}
	s.logSearchMode(ctx, run, mode, len(hits), limit)
	return hits, mode, nil
}

// HybridSearch объединяет ранги BM25 и векторного поиска.
func (s Service) HybridSearch(ctx context.Context, run pgtype.UUID, text string, limit int) ([]store.SearchHit, string, error) {
	if s.Pool == nil {
		return nil, "", fmt.Errorf("нет подключения к PostgreSQL")
	}
	hits, mode, err := store.New(s.Pool).SearchHybrid(ctx, run, text, s.Model.Model, limit, s.embed)
	if err != nil {
		return nil, mode, err
	}
	s.logSearchMode(ctx, run, mode, len(hits), limit)
	return hits, mode, nil
}

// logSearchMode фиксирует фактический режим поиска. Если Ollama настроена, но
// запрос ушёл в BM25, это предупреждение: значит эмбеддинг запроса не удался и
// качество поиска деградировало до лексического.
func (s Service) logSearchMode(ctx context.Context, run pgtype.UUID, mode string, hits, limit int) {
	if ctx.Err() != nil {
		return
	}
	logger := logging.L()
	if mode != "vector" && mode != "hybrid" && strings.TrimSpace(s.Model.BaseURL) != "" && strings.TrimSpace(s.Model.Model) != "" {
		logger.Warn("search_vector_fallback",
			"mode", mode,
			"model", s.Model.Model,
			"run_id", run.String(),
			"limit", limit,
			"hits", hits)
		return
	}
	logger.Debug("search_completed",
		"mode", mode,
		"run_id", run.String(),
		"limit", limit,
		"hits", hits)
}
