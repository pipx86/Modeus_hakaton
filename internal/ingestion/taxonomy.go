package ingestion

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TaxonomyItem struct {
	Code       string `json:"code"`
	Title      string `json:"title"`
	ParentCode string `json:"parent_code"`
}

type Taxonomy struct {
	Name    string         `json:"name"`
	Version string         `json:"version"`
	Items   []TaxonomyItem `json:"items"`
}

func ReadTaxonomy(r io.Reader, format, name string) (Taxonomy, error) {
	var result Taxonomy
	switch strings.ToLower(format) {
	case "json":
		data, err := io.ReadAll(io.LimitReader(r, 20<<20+1))
		if err != nil {
			return result, err
		}
		if len(data) > 20<<20 {
			return result, errors.New("файл больше 20 МБ")
		}
		if err = json.Unmarshal(data, &result); err != nil {
			return result, fmt.Errorf("JSON: %w", err)
		}
	case "csv":
		data, err := io.ReadAll(io.LimitReader(r, 20<<20+1))
		if err != nil {
			return result, err
		}
		if len(data) > 20<<20 {
			return result, errors.New("файл больше 20 МБ")
		}
		reader := csv.NewReader(strings.NewReader(string(data)))
		header, err := reader.Read()
		if err != nil {
			return result, err
		}
		cols := map[string]int{}
		for i, h := range header {
			cols[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))] = i
		}
		if _, ok := cols["code"]; !ok {
			return result, errors.New("в CSV нет code")
		}
		if _, ok := cols["title"]; !ok {
			return result, errors.New("в CSV нет title")
		}
		result.Name = name
		for line := 2; ; line++ {
			row, e := reader.Read()
			if e == io.EOF {
				break
			}
			if e != nil {
				return result, fmt.Errorf("строка %d: %w", line, e)
			}
			get := func(k string) string {
				if i, ok := cols[k]; ok && i < len(row) {
					return row[i]
				}
				return ""
			}
			result.Items = append(result.Items, TaxonomyItem{Code: get("code"), Title: get("title"), ParentCode: get("parent_code")})
		}
	default:
		return result, errors.New("поддерживаются только json и csv")
	}
	result.Name = strings.TrimSpace(result.Name)
	result.Version = strings.TrimSpace(result.Version)
	if result.Name == "" || len(result.Items) == 0 {
		return result, errors.New("укажите название и хотя бы один элемент справочника")
	}
	byCode := make(map[string]TaxonomyItem, len(result.Items))
	for i := range result.Items {
		v := &result.Items[i]
		v.Code = strings.TrimSpace(v.Code)
		v.Title = strings.TrimSpace(v.Title)
		v.ParentCode = strings.TrimSpace(v.ParentCode)
		if v.Code == "" || v.Title == "" {
			return result, fmt.Errorf("элемент %d: обязательны code и title", i+1)
		}
		if _, ok := byCode[v.Code]; ok {
			return result, fmt.Errorf("повтор кода %s", v.Code)
		}
		byCode[v.Code] = *v
	}
	for _, v := range result.Items {
		seen := map[string]bool{}
		current := v.Code
		for current != "" {
			if seen[current] {
				return result, fmt.Errorf("цикл у элемента %s", v.Code)
			}
			seen[current] = true
			item, ok := byCode[current]
			if !ok {
				return result, fmt.Errorf("не найден родитель %s", current)
			}
			current = item.ParentCode
		}
	}
	return result, nil
}

// SaveTaxonomy добавляет или обновляет элементы по кодам в одной транзакции.
func SaveTaxonomy(ctx context.Context, pool *pgxpool.Pool, key string, data Taxonomy) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("нужен постоянный ключ источника")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "taxonomy:"+key); err != nil {
		return "", err
	}
	var source string
	err = tx.QueryRow(ctx, `INSERT INTO sources(type,title,provider,external_id) VALUES('document',$1,'file',$2)
ON CONFLICT (provider,type,external_id) WHERE provider IS NOT NULL AND external_id IS NOT NULL
DO UPDATE SET title=EXCLUDED.title,updated_at=now() RETURNING id::text`, data.Name, key).Scan(&source)
	if err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRow(ctx, "SELECT id::text FROM taxonomies WHERE source_id=$1 AND name=$2 AND version IS NOT DISTINCT FROM NULLIF($3,'')", source, data.Name, data.Version).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, "INSERT INTO taxonomies(source_id,name,version) VALUES($1,$2,NULLIF($3,'')) RETURNING id::text", source, data.Name, data.Version).Scan(&id)
	}
	if err != nil {
		return "", err
	}
	ids := map[string]string{}
	// Сначала создаём все элементы, затем устанавливаем родительские связи.
	for _, v := range data.Items {
		var itemID string
		err = tx.QueryRow(ctx, `INSERT INTO taxonomy_items(taxonomy_id,code,title) VALUES($1,$2,$3)
ON CONFLICT (taxonomy_id,code) WHERE code IS NOT NULL
DO UPDATE SET title=EXCLUDED.title,updated_at=now() RETURNING id::text`, id, v.Code, v.Title).Scan(&itemID)
		if err != nil {
			return "", err
		}
		ids[v.Code] = itemID
	}
	for _, v := range data.Items {
		var parent any
		if v.ParentCode != "" {
			parent = ids[v.ParentCode]
		}
		if _, err = tx.Exec(ctx, "UPDATE taxonomy_items SET parent_id=$2 WHERE id=$1", ids[v.Code], parent); err != nil {
			return "", err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}
