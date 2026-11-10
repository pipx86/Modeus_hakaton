package search

import (
	"activity-descriptor/internal/embeddings"
	"activity-descriptor/internal/store"
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"strconv"
	"strings"
)

type Service struct {
	Pool  *pgxpool.Pool
	Model embeddings.Ollama
}

// IndexSource сохраняет фрагменты и векторы одной модели. Старые векторы при сбое не теряются.
func (s Service) IndexSource(ctx context.Context, id pgtype.UUID) error {
	if !id.Valid {
		return errors.New("нужен ID источника")
	}
	var text string
	if err := s.Pool.QueryRow(ctx, "SELECT COALESCE(content,'') FROM sources WHERE id=$1", id).Scan(&text); err != nil {
		return err
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return errors.New("источник не содержит текста")
	}
	var quotes []string
	var vectors []string
	for start := 0; start < len(runes); {
		end := start + 800
		if end > len(runes) {
			end = len(runes)
		}
		quote := string(runes[start:end])
		vector, err := s.Model.Embed(ctx, quote)
		if err != nil {
			return err
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
		start = end - 100
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	// Источник мог измениться во время работы модели: не записываем устаревшие векторы.
	var current string
	if err = tx.QueryRow(ctx, "SELECT COALESCE(content,'') FROM sources WHERE id=$1 FOR UPDATE", id).Scan(&current); err != nil {
		return err
	}
	if current != text {
		return errors.New("источник изменился во время векторизации; повторите операцию")
	}
	if _, err = tx.Exec(ctx, "DELETE FROM source_fragments WHERE source_id=$1", id); err != nil {
		return err
	}
	for i, quote := range quotes {
		var fragment pgtype.UUID
		if err = tx.QueryRow(ctx, "INSERT INTO source_fragments(source_id,ordinal,quote) VALUES($1,$2,$3) RETURNING id", id, i, quote).Scan(&fragment); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO fragment_embeddings(fragment_id,model,embedding) VALUES($1,$2,$3::vector)", fragment, s.Model.Model, vectors[i]); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Search использует реальный клиент модели; store переключается на BM25 при его сбое.
func (s Service) Search(ctx context.Context, run pgtype.UUID, text string, limit int) ([]store.SearchHit, string, error) {
	if s.Pool == nil {
		return nil, "", fmt.Errorf("нет подключения к PostgreSQL")
	}
	return store.New(s.Pool).SearchFragments(ctx, run, text, s.Model.Model, limit, s.Model.Embed)
}

// HybridSearch объединяет ранги BM25 и векторного поиска.
func (s Service) HybridSearch(ctx context.Context, run pgtype.UUID, text string, limit int) ([]store.SearchHit, string, error) {
	return store.New(s.Pool).SearchHybrid(ctx, run, text, s.Model.Model, limit, s.Model.Embed)
}
