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
	"unicode"

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

var (
	vacancyScriptPattern = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
	vacancyStylePattern  = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`)
	vacancyBlockPattern  = regexp.MustCompile(`(?i)</?(?:address|article|blockquote|br|dd|div|dl|dt|fieldset|figcaption|figure|footer|h[1-6]|header|hr|li|main|ol|p|pre|section|table|tbody|td|tfoot|th|thead|tr|ul)\b[^>]*>`)
	vacancyTagPattern    = regexp.MustCompile(`<[^>]*>`)
	vacancyHeading       = regexp.MustCompile(`(?i)^\s*(чем предстоит заниматься|задачи и обязанности|обязанности|задачи|функционал|responsibilities|что нужно знать|ключевые навыки|требования|мы ждем|мы ждём|skills|условия работы|что мы предлагаем|мы предлагаем|условия|бонусы|о компании)\s*[:：-]?\s*(.*)$`)
)

const (
	vacancySectionDescription  = "description"
	vacancySectionDuties       = "duties"
	vacancySectionRequirements = "requirements"
	vacancySectionConditions   = "conditions"
	vacancySectionCompany      = "company"
)

func cleanVacancyText(value string) string {
	value = vacancyScriptPattern.ReplaceAllString(value, "\n")
	value = vacancyStylePattern.ReplaceAllString(value, "\n")
	value = vacancyBlockPattern.ReplaceAllString(value, "\n")
	value = vacancyTagPattern.ReplaceAllString(value, " ")
	value = html.UnescapeString(value)
	value = strings.NewReplacer(
		"\r\n", "\n",
		"\r", "\n",
		"\u00a0", " ",
		"\u200b", "",
		"\ufeff", "",
	).Replace(value)
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return ' '
		}
		return r
	}, value)

	lines := strings.Split(value, "\n")
	normalized := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if len(normalized) > 0 && !blank {
				normalized = append(normalized, "")
			}
			blank = true
			continue
		}
		normalized = append(normalized, line)
		blank = false
	}
	return strings.TrimSpace(strings.Join(normalized, "\n"))
}

func (v Vacancy) content() string {
	if description := normalizeVacancyDescription(v.Description); description != "" {
		return description
	}
	parts := []string{
		formatVacancySection(vacancySectionRequirements, v.Snippet.Requirement),
		formatVacancySection(vacancySectionDuties, v.Snippet.Responsibility),
	}
	return strings.Join(nonEmptyVacancyParts(parts), "\n\n")
}

// normalizeVacancyTitle cleans a display title without changing its wording.
func normalizeVacancyTitle(value string) string {
	return normalizeVacancyLabel(value)
}

// normalizeVacancyLabel cleans a short provider field while preserving its
// wording and display case.
func normalizeVacancyLabel(value string) string {
	return strings.Join(strings.Fields(cleanVacancyText(value)), " ")
}

// normalizeVacancyKey is for comparisons and deduplication only. Keep the
// cleaned display value separately so normalization does not erase casing.
func normalizeVacancyKey(value string) string {
	return strings.ToLower(normalizeVacancyLabel(value))
}

func normalizeVacancySkillLabels(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		label := normalizeVacancyLabel(value)
		key := normalizeVacancyKey(label)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, label)
	}
	return result
}

// normalizeVacancyDescription cleans markup and labels recognized sections.
// It deliberately retains every section, including conditions and company
// information, so normalization never silently destroys source material.
func normalizeVacancyDescription(value string) string {
	cleaned := cleanVacancyText(value)
	if cleaned == "" {
		return ""
	}

	type section struct {
		kind  string
		lines []string
	}
	sections := make([]section, 0, 4)
	current := -1
	recognized := false
	for _, line := range strings.Split(cleaned, "\n") {
		matches := vacancyHeading.FindStringSubmatch(line)
		if matches == nil {
			if current < 0 {
				sections = append(sections, section{kind: vacancySectionDescription})
				current = len(sections) - 1
			}
			sections[current].lines = append(sections[current].lines, line)
			continue
		}

		kind := vacancySectionKind(matches[1])
		if kind == "" {
			if current < 0 {
				sections = append(sections, section{kind: vacancySectionDescription})
				current = len(sections) - 1
			}
			sections[current].lines = append(sections[current].lines, line)
			continue
		}
		recognized = true
		sections = append(sections, section{kind: kind})
		current = len(sections) - 1
		if remainder := strings.TrimSpace(matches[2]); remainder != "" {
			sections[current].lines = append(sections[current].lines, remainder)
		}
	}
	if !recognized {
		return cleaned
	}

	parts := make([]string, 0, len(sections))
	for _, item := range sections {
		body := cleanVacancyText(strings.Join(item.lines, "\n"))
		if body == "" {
			continue
		}
		label := vacancySectionLabel(item.kind)
		if label == "" {
			parts = append(parts, body)
			continue
		}
		parts = append(parts, label+":\n"+body)
	}
	return strings.Join(parts, "\n\n")
}

func vacancySectionKind(header string) string {
	switch strings.ToLower(strings.TrimSpace(header)) {
	case "обязанности", "чем предстоит заниматься", "задачи и обязанности", "задачи", "функционал", "responsibilities":
		return vacancySectionDuties
	case "требования", "мы ждем", "мы ждём", "что нужно знать", "ключевые навыки", "skills":
		return vacancySectionRequirements
	case "условия работы", "условия", "мы предлагаем", "что мы предлагаем", "бонусы":
		return vacancySectionConditions
	case "о компании":
		return vacancySectionCompany
	default:
		return ""
	}
}

func vacancySectionLabel(kind string) string {
	switch kind {
	case vacancySectionDuties:
		return "Обязанности в вакансии (описание работодателя)"
	case vacancySectionDescription:
		return "Описание вакансии (раздел не распознан)"
	case vacancySectionRequirements:
		return "Требования работодателя (ожидания, не подтверждение действий респондента)"
	case vacancySectionConditions:
		return "Условия работы (контекст, не описание деятельности)"
	case vacancySectionCompany:
		return "О компании (контекст)"
	default:
		return ""
	}
}

func formatVacancySection(kind, value string) string {
	body := cleanVacancyText(value)
	label := vacancySectionLabel(kind)
	if body == "" || label == "" {
		return ""
	}
	return label + ":\n" + body
}

func nonEmptyVacancyParts(parts []string) []string {
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			result = append(result, part)
		}
	}
	return result
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
	if strings.TrimSpace(v.ID) == "" || normalizeVacancyTitle(v.Name) == "" {
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
		v.Name = normalizeVacancyTitle(v.Name)
		v.URL = strings.TrimSpace(v.URL)
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
