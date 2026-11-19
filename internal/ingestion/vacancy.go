package ingestion

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"activity-descriptor/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Vacancy struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	URL             string          `json:"alternate_url"`
	Description     string          `json:"description"`
	PublishedAt     string          `json:"published_at,omitempty"`
	Area            vacancyLabel    `json:"area"`
	Employer        vacancyEmployer `json:"employer"`
	Experience      vacancyLabel    `json:"experience"`
	Salary          vacancySalary   `json:"salary"`
	KeySkills       []vacancyLabel  `json:"key_skills"`
	ExtractedSkills []string        `json:"extracted_skills"`
	RawData         struct {
		ExtractedSkills []string `json:"extracted_skills"`
	} `json:"raw_data"`
	Snippet struct {
		Requirement    string `json:"requirement"`
		Responsibility string `json:"responsibility"`
	} `json:"snippet"`
}

type Counts struct {
	Inserted int `json:"inserted"`
	Updated  int `json:"updated"`
	Skipped  int `json:"skipped"`
}

var vacancyTagPattern = regexp.MustCompile(`<[^>]+>`)

func cleanVacancyText(value string) string {
	return strings.TrimSpace(html.UnescapeString(vacancyTagPattern.ReplaceAllString(value, "")))
}

func (v Vacancy) content() string {
	if description := cleanVacancyText(v.Description); description != "" {
		return description
	}
	return cleanVacancyText(strings.Join([]string{v.Snippet.Requirement, v.Snippet.Responsibility}, "\n"))
}

func (v Vacancy) publicationTime() (pgtype.Timestamptz, error) {
	raw := strings.TrimSpace(v.PublishedAt)
	if raw == "" {
		return pgtype.Timestamptz{}, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05-0700"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			stored := parsed.UTC().Truncate(time.Microsecond)
			return pgtype.Timestamptz{Time: stored, Valid: true}, nil
		}
	}
	return pgtype.Timestamptz{}, errors.New("недопустимый published_at; нужна дата и время с часовым поясом")
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
			v := Vacancy{ID: get("id"), Name: get("name"), URL: get("alternate_url"), Description: get("description"), PublishedAt: get("published_at")}
			if err := v.setCSVMarketFields(get); err != nil {
				return nil, fmt.Errorf("строка %d: %w", n, err)
			}
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

type transactionPool interface {
	Begin(context.Context) (pgx.Tx, error)
}

// SaveVacancies выполняет импорт целиком в одной транзакции и не создаёт дубли.
func SaveVacancies(ctx context.Context, pool transactionPool, vacancies []Vacancy) (Counts, error) {
	var counts Counts
	vacancies = append([]Vacancy(nil), vacancies...)
	sort.SliceStable(vacancies, func(i, j int) bool { return strings.TrimSpace(vacancies[i].ID) < strings.TrimSpace(vacancies[j].ID) })
	tx, err := pool.Begin(ctx)
	if err != nil {
		return counts, err
	}
	defer tx.Rollback(context.Background())
	queries := db.New(tx)
	for _, v := range vacancies {
		if err := v.validate(); err != nil {
			return Counts{}, err
		}
		v.ID = strings.TrimSpace(v.ID)
		v.Name = strings.TrimSpace(v.Name)
		published, parseErr := v.publicationTime()
		if parseErr != nil {
			return Counts{}, fmt.Errorf("вакансия %s: %w", v.ID, parseErr)
		}
		// Блокировка защищает счётчики вставок и обновлений при параллельном импорте.
		if err := queries.LockVacancyImport(ctx, "hh:vacancy:"+v.ID); err != nil {
			return Counts{}, err
		}
		marketDetails, err := v.marketDetails()
		if err != nil {
			return Counts{}, err
		}
		content := v.content()
		unchanged := false
		current, err := queries.GetVacancyForImport(ctx, v.ID)
		dateUnchanged := !published.Valid || (current.VacancyPublishedAt.Valid && current.VacancyPublishedAt.Time.Equal(published.Time))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			err = queries.CreateImportedVacancy(ctx, db.CreateImportedVacancyParams{
				Title: v.Name, Content: content, SourceUrl: v.URL,
				ExternalID: v.ID, VacancyPublishedAt: published,
			})
			counts.Inserted++
		case err != nil:
			return Counts{}, err
		case current.Title == v.Name && current.Content == content && current.SourceUrl == v.URL && dateUnchanged:
			unchanged = true
			counts.Skipped++
		default:
			err = queries.UpdateImportedVacancy(ctx, db.UpdateImportedVacancyParams{
				ID: current.ID, Title: v.Name, Content: content, SourceUrl: v.URL, VacancyPublishedAt: published,
			})
			counts.Updated++
		}
		if err != nil {
			return Counts{}, err
		}
		if len(marketDetails) > 0 {
			changed, err := queries.MergeVacancyMarketDetails(ctx, db.MergeVacancyMarketDetailsParams{
				Details: marketDetails, ExternalID: v.ID,
			})
			if err != nil {
				return Counts{}, err
			}
			if unchanged && changed > 0 {
				counts.Skipped--
				counts.Updated++
			}
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
