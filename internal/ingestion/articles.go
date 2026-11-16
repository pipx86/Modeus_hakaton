package ingestion

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Article struct {
	DOI      string
	Title    string
	Abstract string
}

// FetchArticles получает метаданные статей Crossref по явно заданной теме.
func FetchArticles(ctx context.Context, client *http.Client, endpoint, query string, limit int) ([]Article, error) {
	if strings.TrimSpace(query) == "" || limit < 1 || limit > 100 {
		return nil, &domain.Error{Code: "INVALID_ARTICLE_QUERY", Message: "нужны тема поиска и количество от 1 до 100"}
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("неверный адрес Crossref")
	}
	values := u.Query()
	values.Set("query", query)
	values.Set("rows", strconv.Itoa(limit))
	values.Set("filter", "type:journal-article")
	u.RawQuery = values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ModeusResearch/1.0")
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("запрос Crossref: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Crossref вернул HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 8<<20+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 8<<20 {
		return nil, errors.New("ответ Crossref больше 8 МБ")
	}
	var response struct {
		Message struct {
			Items []struct {
				DOI      string   `json:"DOI"`
				Title    []string `json:"title"`
				Abstract string   `json:"abstract"`
			} `json:"items"`
		} `json:"message"`
	}
	if err := json.Unmarshal(data, &response); err != nil || response.Message.Items == nil {
		return nil, errors.New("неверный ответ Crossref")
	}
	result := make([]Article, 0, len(response.Message.Items))
	seen := map[string]bool{}
	for _, item := range response.Message.Items {
		doi := strings.ToLower(strings.TrimSpace(item.DOI))
		if len(item.Title) == 0 || doi == "" || strings.TrimSpace(item.Title[0]) == "" || seen[doi] {
			continue
		}
		seen[doi] = true
		result = append(result, Article{DOI: doi, Title: strings.TrimSpace(item.Title[0]), Abstract: abstractText(item.Abstract)})
	}
	return result, nil
}

func abstractText(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	d := xml.NewDecoder(strings.NewReader("<root>" + value + "</root>"))
	var parts []string
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ""
		}
		if text, ok := token.(xml.CharData); ok {
			parts = append(parts, string(text))
		}
	}
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

// SaveArticles сохраняет DOI как внешний идентификатор и при необходимости связывает статьи с исследованием.
func SaveArticles(ctx context.Context, pool *pgxpool.Pool, items []Article, runID string) (Counts, error) {
	var counts Counts
	tx, err := pool.Begin(ctx)
	if err != nil {
		return counts, err
	}
	defer tx.Rollback(context.Background())
	for _, article := range items {
		doi := strings.ToLower(strings.TrimSpace(article.DOI))
		title := strings.TrimSpace(article.Title)
		if doi == "" || title == "" {
			return Counts{}, errors.New("у статьи нужны DOI и название")
		}
		content := title
		if article.Abstract != "" {
			content += "\n" + article.Abstract
		}
		sourceURL := "https://doi.org/" + doi
		var id string
		err = tx.QueryRow(ctx, `INSERT INTO sources(type,title,content,source_url,provider,external_id) VALUES('document',$1,$2,$3,'crossref',$4) ON CONFLICT (provider,type,external_id) WHERE provider IS NOT NULL AND external_id IS NOT NULL DO NOTHING RETURNING id::text`, title, content, sourceURL, doi).Scan(&id)
		if err == nil {
			counts.Inserted++
		} else if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `UPDATE sources SET title=$2,content=$3,source_url=$4,updated_at=now() WHERE provider='crossref' AND type='document' AND external_id=$1 AND (title,content,source_url) IS DISTINCT FROM ($2,$3,$4) RETURNING id::text`, doi, title, content, sourceURL).Scan(&id)
			if err == nil {
				counts.Updated++
			} else if errors.Is(err, pgx.ErrNoRows) {
				err = tx.QueryRow(ctx, `SELECT id::text FROM sources WHERE provider='crossref' AND type='document' AND external_id=$1`, doi).Scan(&id)
				counts.Skipped++
			}
		}
		if err != nil {
			return Counts{}, err
		}
		if strings.TrimSpace(runID) != "" {
			if _, err = tx.Exec(ctx, `INSERT INTO run_sources(run_id,source_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, runID, id); err != nil {
				return Counts{}, err
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Counts{}, err
	}
	return counts, nil
}
