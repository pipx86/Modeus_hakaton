package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	// DefaultTrudvsemVacanciesURL is the public open-data endpoint documented
	// by «Работа России». It does not require credentials.
	DefaultTrudvsemVacanciesURL = "https://opendata.trudvsem.ru/api/v1/vacancies"
	trudvsemProvider            = "trudvsem"
	trudvsemPageSize            = 100
	trudvsemResponseLimit       = 20 << 20
)

// TrudvsemVacancy includes only fields needed to analyse professional
// activities. The public API also returns a contact person and phone numbers;
// they are deliberately absent from this type and never written to storage.
type TrudvsemVacancy struct {
	ID           string `json:"id"`
	JobName      string `json:"job-name"`
	URL          string `json:"vac_url"`
	Requirements string `json:"requirements"`
	Duty         string `json:"duty"`
	CreatedAt    string `json:"creation-date"`
	ModifiedAt   string `json:"date_modify"`
	Company      struct {
		Name string `json:"name"`
	} `json:"company"`
	Region struct {
		Name string `json:"name"`
	} `json:"region"`
}

func (v TrudvsemVacancy) validate() error {
	if strings.TrimSpace(v.ID) == "" || strings.TrimSpace(v.JobName) == "" {
		return errors.New("у вакансии «Работы России» обязательны id и job-name")
	}
	return nil
}

func (v TrudvsemVacancy) title() string {
	return "[Работа России] " + strings.TrimSpace(v.JobName)
}

func (v TrudvsemVacancy) content() string {
	parts := make([]string, 0, 2)
	if value := cleanVacancyText(v.Requirements); value != "" {
		parts = append(parts, "Требования:\n"+value)
	}
	if value := cleanVacancyText(v.Duty); value != "" {
		parts = append(parts, "Обязанности:\n"+value)
	}
	return strings.Join(parts, "\n\n")
}

func (v TrudvsemVacancy) publicationTime() (time.Time, bool, error) {
	raw := strings.TrimSpace(v.CreatedAt)
	if raw == "" {
		return time.Time{}, false, nil
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02T15:04:05-0700"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC().Truncate(time.Microsecond), true, nil
		}
	}
	return time.Time{}, false, errors.New("недопустимый creation-date")
}

type trudvsemResponse struct {
	Status string `json:"status"`
	Meta   struct {
		Total int `json:"total"`
	} `json:"meta"`
	Results struct {
		Vacancies []struct {
			Vacancy TrudvsemVacancy `json:"vacancy"`
		} `json:"vacancies"`
	} `json:"results"`
}

// FetchTrudvsem reads public vacancy pages from the official open-data API.
// It never follows vacancy pages and never requests contact details separately.
func FetchTrudvsem(ctx context.Context, client *http.Client, endpoint, query, userAgent string, maxPages int) ([]TrudvsemVacancy, error) {
	if strings.TrimSpace(query) == "" || maxPages < 1 || maxPages > 20 {
		return nil, errors.New("нужны поисковый запрос и число страниц от 1 до 20")
	}
	base, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, errors.New("неверный адрес API «Работы России»")
	}
	if client == nil {
		client = DefaultHTTPClient()
	}

	all := make([]TrudvsemVacancy, 0, maxPages*trudvsemPageSize)
	seen := make(map[string]struct{})
	for page := 0; page < maxPages; page++ {
		u := *base
		params := u.Query()
		params.Set("text", query)
		params.Set("limit", strconv.Itoa(trudvsemPageSize))
		params.Set("offset", strconv.Itoa(page*trudvsemPageSize))
		u.RawQuery = params.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("создать запрос «Работы России»: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		if userAgent = strings.TrimSpace(userAgent); userAgent != "" {
			req.Header.Set("User-Agent", userAgent)
		}
		res, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("запрос «Работы России»: %w", err)
		}
		data, readErr := io.ReadAll(io.LimitReader(res.Body, trudvsemResponseLimit+1))
		res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("прочитать ответ «Работы России»: %w", readErr)
		}
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("«Работа России» вернула HTTP %d", res.StatusCode)
		}
		if len(data) > trudvsemResponseLimit {
			return nil, errors.New("ответ «Работы России» больше 20 МБ")
		}

		var batch trudvsemResponse
		if err := json.Unmarshal(data, &batch); err != nil {
			return nil, errors.New("неверный JSON-ответ «Работы России»")
		}
		if batch.Status != "200" {
			return nil, fmt.Errorf("«Работа России» вернула статус %q", batch.Status)
		}
		for _, item := range batch.Results.Vacancies {
			vacancy := item.Vacancy
			if err := vacancy.validate(); err != nil {
				return nil, err
			}
			if _, exists := seen[vacancy.ID]; exists {
				continue
			}
			seen[vacancy.ID] = struct{}{}
			all = append(all, vacancy)
		}
		if len(batch.Results.Vacancies) == 0 || (batch.Meta.Total > 0 && len(all) >= batch.Meta.Total) {
			break
		}
	}
	return all, nil
}

// SaveTrudvsemVacancies saves the selected public vacancy fields transactionally
// and keeps provider identity separate from hh.ru records.
func SaveTrudvsemVacancies(ctx context.Context, pool transactionPool, vacancies []TrudvsemVacancy) (Counts, error) {
	var counts Counts
	vacancies = append([]TrudvsemVacancy(nil), vacancies...)
	sort.SliceStable(vacancies, func(i, j int) bool { return strings.TrimSpace(vacancies[i].ID) < strings.TrimSpace(vacancies[j].ID) })

	tx, err := pool.Begin(ctx)
	if err != nil {
		return counts, err
	}
	defer tx.Rollback(context.Background())

	for _, vacancy := range vacancies {
		if err := vacancy.validate(); err != nil {
			return Counts{}, err
		}
		vacancy.ID = strings.TrimSpace(vacancy.ID)
		publishedAt, hasPublishedAt, err := vacancy.publicationTime()
		if err != nil {
			return Counts{}, fmt.Errorf("вакансия %s: %w", vacancy.ID, err)
		}
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, trudvsemProvider+":vacancy:"+vacancy.ID); err != nil {
			return Counts{}, err
		}

		var currentID, currentTitle, currentContent, currentURL string
		var currentPublishedAt *time.Time
		err = tx.QueryRow(ctx, `
			SELECT id::text, title, COALESCE(content, '')::text, COALESCE(source_url, '')::text, vacancy_published_at
			FROM sources
			WHERE type = 'vacancy' AND provider = $1 AND external_id = $2`, trudvsemProvider, vacancy.ID,
		).Scan(&currentID, &currentTitle, &currentContent, &currentURL, &currentPublishedAt)

		title := vacancy.title()
		content := vacancy.content()
		url := strings.TrimSpace(vacancy.URL)
		unchanged := err == nil && currentTitle == title && currentContent == content && currentURL == url &&
			(!hasPublishedAt || (currentPublishedAt != nil && currentPublishedAt.Equal(publishedAt)))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			var published any
			if hasPublishedAt {
				published = publishedAt
			}
			_, err = tx.Exec(ctx, `
				INSERT INTO sources (type, title, content, source_url, provider, external_id, vacancy_published_at)
				VALUES ('vacancy', $1, $2, $3, $4, $5, $6)`, title, content, url, trudvsemProvider, vacancy.ID, published)
			counts.Inserted++
		case err != nil:
			return Counts{}, err
		case unchanged:
			counts.Skipped++
		default:
			var published any
			if hasPublishedAt {
				published = publishedAt
			}
			_, err = tx.Exec(ctx, `
				UPDATE sources
				SET title = $1, content = $2, source_url = $3,
					vacancy_published_at = COALESCE($4::timestamptz, vacancy_published_at), updated_at = now()
				WHERE id = $5::uuid`, title, content, url, published, currentID)
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
