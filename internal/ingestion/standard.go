package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ledongthuc/pdf"
)

type Standard struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	URL     string `json:"url"`
}

// ReadStandard извлекает текстовый слой PDF или читает JSON документа.
func ReadStandard(path, format, title string) (result Standard, err error) {
	defer func() {
		if recover() != nil {
			result = Standard{}
			err = errors.New("не удалось разобрать повреждённый PDF")
		}
	}()
	info, err := os.Stat(path)
	if err != nil {
		return result, err
	}
	if info.Size() > 20<<20 {
		return result, errors.New("файл больше 20 МБ")
	}
	switch strings.ToLower(format) {
	case "json":
		data, e := os.ReadFile(path)
		if e != nil {
			return result, e
		}
		if e = json.Unmarshal(data, &result); e != nil {
			return result, fmt.Errorf("JSON: %w", e)
		}
	case "pdf":
		f, r, e := pdf.Open(path)
		if e != nil {
			return result, fmt.Errorf("PDF: %w", e)
		}
		defer f.Close()
		text, e := r.GetPlainText()
		if e != nil {
			return result, fmt.Errorf("текст PDF: %w", e)
		}
		data, e := io.ReadAll(io.LimitReader(text, 20<<20+1))
		if e != nil {
			return result, e
		}
		if len(data) > 20<<20 {
			return result, errors.New("текст PDF больше 20 МБ")
		}
		result = Standard{Title: title, Content: string(data)}
	default:
		return result, errors.New("профстандарт поддерживает pdf и json")
	}
	result.Title = strings.TrimSpace(result.Title)
	result.Content = strings.TrimSpace(result.Content)
	if result.Title == "" || result.Content == "" {
		return result, errors.New("нужны название и текст; для скана PDF требуется отдельное распознавание")
	}
	return result, nil
}

func SaveStandard(ctx context.Context, pool *pgxpool.Pool, key string, s Standard) (Counts, error) {
	var c Counts
	key = strings.TrimSpace(key)
	if key == "" || strings.TrimSpace(s.Title) == "" || strings.TrimSpace(s.Content) == "" {
		return c, errors.New("нужны ключ, название и текст документа")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return c, err
	}
	defer tx.Rollback(context.Background())
	key = "standard:" + key
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", key); err != nil {
		return c, err
	}
	var id, title, content, link string
	err = tx.QueryRow(ctx, "SELECT id::text,title,COALESCE(content,''),COALESCE(source_url,'') FROM sources WHERE provider='file' AND type='standard' AND external_id=$1", key).Scan(&id, &title, &content, &link)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		_, err = tx.Exec(ctx, "INSERT INTO sources(type,title,content,source_url,provider,external_id) VALUES('standard',$1,$2,$3,'file',$4)", s.Title, s.Content, s.URL, key)
		c.Inserted = 1
	case err != nil:
		return Counts{}, err
	case title == s.Title && content == s.Content && link == s.URL:
		c.Skipped = 1
	default:
		_, err = tx.Exec(ctx, "UPDATE sources SET title=$2,content=$3,source_url=$4,updated_at=now() WHERE id=$1", id, s.Title, s.Content, s.URL)
		c.Updated = 1
	}
	if err != nil {
		return Counts{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Counts{}, err
	}
	return c, nil
}
