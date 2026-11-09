package ingestion

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Vacancy struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"alternate_url"`
	Description string `json:"description"`
	Snippet     struct {
		Requirement    string `json:"requirement"`
		Responsibility string `json:"responsibility"`
	} `json:"snippet"`
}

type Counts struct {
	Inserted int `json:"inserted"`
	Updated  int `json:"updated"`
	Skipped  int `json:"skipped"`
}

func (v Vacancy) content() string {
	if v.Description != "" {
		return v.Description
	}
	return strings.TrimSpace(strings.Join([]string{v.Snippet.Requirement, v.Snippet.Responsibility}, "\n"))
}

func (v Vacancy) validate() error {
	if strings.TrimSpace(v.ID) == "" || strings.TrimSpace(v.Name) == "" {
		return errors.New("у вакансии обязательны id и name")
	}
	return nil
}

// ReadVacancies читает массив JSON либо объект ответа hh.ru с полем items.
func ReadVacancies(r io.Reader, format string) ([]Vacancy, error) {
	switch strings.ToLower(format) {
	case "json":
		data, err := io.ReadAll(io.LimitReader(r, 20<<20+1))
		if err != nil {
			return nil, err
		}
		if len(data) > 20<<20 {
			return nil, errors.New("файл больше 20 МБ")
		}
		var list []Vacancy
		if err = json.Unmarshal(data, &list); err != nil {
			var response struct {
				Items []Vacancy `json:"items"`
			}
			if e := json.Unmarshal(data, &response); e != nil || response.Items == nil {
				return nil, errors.New("ожидается массив вакансий или объект с items")
			}
			list = response.Items
		}
		for i, v := range list {
			if err := v.validate(); err != nil {
				return nil, fmt.Errorf("вакансия %d: %w", i+1, err)
			}
		}
		return list, nil
	case "csv":
		data, err := io.ReadAll(io.LimitReader(r, 20<<20+1))
		if err != nil {
			return nil, err
		}
		if len(data) > 20<<20 {
			return nil, errors.New("файл больше 20 МБ")
		}
		reader := csv.NewReader(strings.NewReader(string(data)))
		header, err := reader.Read()
		if err != nil {
			return nil, fmt.Errorf("заголовок CSV: %w", err)
		}
		cols := map[string]int{}
		for i, s := range header {
			cols[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(s, "\ufeff")))] = i
		}
		if _, ok := cols["id"]; !ok {
			return nil, errors.New("в CSV нет столбца id")
		}
		if _, ok := cols["name"]; !ok {
			return nil, errors.New("в CSV нет столбца name")
		}
		var list []Vacancy
		for n := 2; ; n++ {
			row, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("строка %d: %w", n, err)
			}
			get := func(key string) string {
				if i, ok := cols[key]; ok && i < len(row) {
					return row[i]
				}
				return ""
			}
			v := Vacancy{ID: get("id"), Name: get("name"), URL: get("alternate_url"), Description: get("description")}
			if err := v.validate(); err != nil {
				return nil, fmt.Errorf("строка %d: %w", n, err)
			}
			list = append(list, v)
		}
		return list, nil
	default:
		return nil, errors.New("поддерживаются только форматы json и csv")
	}
}

// FetchHH получает страницы поиска, не запрашивая персональные данные.
func FetchHH(ctx context.Context, client *http.Client, endpoint, query, userAgent, token string, maxPages int) ([]Vacancy, error) {
	if strings.TrimSpace(query) == "" || strings.TrimSpace(userAgent) == "" || maxPages < 1 || maxPages > 20 {
		return nil, errors.New("нужны поисковый запрос, HH-User-Agent и число страниц от 1 до 20")
	}
	base, err := url.Parse(endpoint)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, errors.New("неверный адрес API")
	}
	var all []Vacancy
	for page := 0; page < maxPages; page++ {
		u := *base
		q := u.Query()
		q.Set("text", query)
		q.Set("per_page", "100")
		q.Set("page", strconv.Itoa(page))
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("HH-User-Agent", userAgent)
		req.Header.Set("User-Agent", userAgent)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("запрос hh.ru: %w", err)
		}
		data, readErr := io.ReadAll(io.LimitReader(res.Body, 20<<20+1))
		res.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("hh.ru вернул HTTP %d", res.StatusCode)
		}
		if len(data) > 20<<20 {
			return nil, errors.New("ответ hh.ru больше 20 МБ")
		}
		var batch struct {
			Items []Vacancy `json:"items"`
			Pages int       `json:"pages"`
		}
		if err := json.Unmarshal(data, &batch); err != nil || batch.Items == nil {
			return nil, errors.New("неверный ответ hh.ru")
		}
		for _, v := range batch.Items {
			if err := v.validate(); err != nil {
				return nil, err
			}
			all = append(all, v)
		}
		if page+1 >= batch.Pages || len(batch.Items) == 0 {
			break
		}
	}
	return all, nil
}

// SaveVacancies выполняет импорт целиком в одной транзакции и не создаёт дубли.
func SaveVacancies(ctx context.Context, pool *pgxpool.Pool, vacancies []Vacancy) (Counts, error) {
	var counts Counts
	vacancies = append([]Vacancy(nil), vacancies...)
	sort.SliceStable(vacancies, func(i, j int) bool { return strings.TrimSpace(vacancies[i].ID) < strings.TrimSpace(vacancies[j].ID) })
	tx, err := pool.Begin(ctx)
	if err != nil {
		return counts, err
	}
	defer tx.Rollback(context.Background())
	for _, v := range vacancies {
		if err := v.validate(); err != nil {
			return Counts{}, err
		}
		v.ID = strings.TrimSpace(v.ID)
		v.Name = strings.TrimSpace(v.Name)
		// Блокировка не даёт двум загрузчикам одновременно классифицировать одну запись.
		if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "hh:vacancy:"+v.ID); err != nil {
			return Counts{}, err
		}
		var id, name, content, link string
		err = tx.QueryRow(ctx, "SELECT id::text,title,COALESCE(content,''),COALESCE(source_url,'') FROM sources WHERE provider='hh' AND type='vacancy' AND external_id=$1", v.ID).Scan(&id, &name, &content, &link)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			_, err = tx.Exec(ctx, "INSERT INTO sources(type,title,content,source_url,provider,external_id) VALUES('vacancy',$1,$2,$3,'hh',$4)", v.Name, v.content(), v.URL, v.ID)
			counts.Inserted++
		case err != nil:
			return Counts{}, err
		case name == v.Name && content == v.content() && link == v.URL:
			counts.Skipped++
		default:
			_, err = tx.Exec(ctx, "UPDATE sources SET title=$2,content=$3,source_url=$4,updated_at=now() WHERE id=$1", id, v.Name, v.content(), v.URL)
			counts.Updated++
		}
		if err != nil {
			return Counts{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Counts{}, err
	}
	return counts, nil
}

func DefaultHTTPClient() *http.Client { return &http.Client{Timeout: 20 * time.Second} }

func ReadVacancyFile(path, format string) ([]Vacancy, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ReadVacancies(f, format)
}
