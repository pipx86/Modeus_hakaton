// Package githubprofile reads public GitHub repositories and turns them into a
// transparent specialist profile: languages, topics, project quality and an
// optional match against the requested profession.
package githubprofile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"activity-descriptor/internal/analysis"
)

var (
	ErrNotFound    = errors.New("github: profile not found")
	ErrRateLimited = errors.New("github: rate limit exceeded")
	ErrUnavailable = errors.New("github: unavailable")
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

// Client reads GitHub's public REST API. Token is optional; without it GitHub
// applies the anonymous rate limit.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	Token   string
}

// New returns a client with a bounded timeout.
func New(token string) *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 20 * time.Second},
		BaseURL: "https://api.github.com",
		Token:   strings.TrimSpace(token),
	}
}

type Language struct {
	Name         string `json:"name"`
	Repositories int    `json:"repositories"`
}

type Project struct {
	Name        string   `json:"name"`
	URL         string   `json:"url"`
	Description string   `json:"description,omitempty"`
	Language    string   `json:"language,omitempty"`
	Topics      []string `json:"topics,omitempty"`
	Stars       int      `json:"stars"`
	Forks       int      `json:"forks"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
}

// Assessment is the deterministic evaluation shown to the user.
type Assessment struct {
	Username          string     `json:"username"`
	Name              string     `json:"name,omitempty"`
	ProfileURL        string     `json:"profile_url"`
	AvatarURL         string     `json:"avatar_url,omitempty"`
	Profession        string     `json:"profession,omitempty"`
	Score             int        `json:"score"`
	ProfessionMatch   int        `json:"profession_match"`
	Repositories      int        `json:"repositories"`
	OriginalRepos     int        `json:"original_repositories"`
	TotalStars        int        `json:"total_stars"`
	TotalForks        int        `json:"total_forks"`
	Languages         []Language `json:"languages"`
	Skills            []string   `json:"skills"`
	Projects          []Project  `json:"projects"`
	Summary           string     `json:"summary"`
	Warnings          []string   `json:"warnings,omitempty"`
	FetchedAt         string     `json:"fetched_at"`
	ProfessionMatched bool       `json:"profession_matched"`
}

type githubUser struct {
	Login     string `json:"login"`
	Name      string `json:"name"`
	HTMLURL   string `json:"html_url"`
	AvatarURL string `json:"avatar_url"`
}

type githubRepo struct {
	Name        string   `json:"name"`
	HTMLURL     string   `json:"html_url"`
	Description string   `json:"description"`
	Language    string   `json:"language"`
	Topics      []string `json:"topics"`
	Stars       int      `json:"stargazers_count"`
	Forks       int      `json:"forks_count"`
	Fork        bool     `json:"fork"`
	Archived    bool     `json:"archived"`
	UpdatedAt   string   `json:"updated_at"`
}

func (c *Client) Evaluate(ctx context.Context, username, profession string) (Assessment, error) {
	username = strings.TrimSpace(username)
	if !usernamePattern.MatchString(username) {
		return Assessment{}, fmt.Errorf("github: invalid username")
	}
	var user githubUser
	if err := c.get(ctx, "/users/"+url.PathEscape(username), &user); err != nil {
		return Assessment{}, err
	}
	var repos []githubRepo
	if err := c.get(ctx, "/users/"+url.PathEscape(username)+"/repos?per_page=100&sort=updated&type=owner", &repos); err != nil {
		return Assessment{}, err
	}

	assessment := buildAssessment(user, repos, profession)
	return assessment, nil
}

func (c *Client) get(ctx context.Context, path string, dst any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "Modeus-GitHub-Profile/1.0")
	if c.Token != "" {
		request.Header.Set("Authorization", "Bearer "+c.Token)
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	switch response.StatusCode {
	case http.StatusOK:
		if err := json.Unmarshal(body, dst); err != nil {
			return fmt.Errorf("%w: decode: %v", ErrUnavailable, err)
		}
		return nil
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusForbidden, http.StatusTooManyRequests:
		return ErrRateLimited
	default:
		return fmt.Errorf("%w: status %d", ErrUnavailable, response.StatusCode)
	}
}

func buildAssessment(user githubUser, repos []githubRepo, profession string) Assessment {
	result := Assessment{
		Username:     user.Login,
		Name:         strings.TrimSpace(user.Name),
		ProfileURL:   user.HTMLURL,
		AvatarURL:    user.AvatarURL,
		Profession:   strings.TrimSpace(profession),
		Repositories: len(repos),
		FetchedAt:    time.Now().UTC().Format(time.RFC3339),
		Projects:     make([]Project, 0, len(repos)),
		Languages:    make([]Language, 0),
		Warnings:     make([]string, 0),
	}
	if result.Username == "" {
		result.Username = strings.TrimSpace(user.Login)
	}
	languageCounts := make(map[string]int)
	skillCounts := make(map[string]int)
	texts := make([]string, 0, len(repos))
	for _, repo := range repos {
		if repo.Fork {
			continue
		}
		result.OriginalRepos++
		result.TotalStars += repo.Stars
		result.TotalForks += repo.Forks
		if repo.Language != "" {
			language := canonicalSkill(repo.Language)
			languageCounts[language]++
			skillCounts[language]++
		}
		for _, topic := range repo.Topics {
			topic = strings.TrimSpace(topic)
			if topic != "" {
				skillCounts[canonicalSkill(topic)]++
			}
		}
		text := strings.Join([]string{repo.Name, repo.Description, repo.Language, strings.Join(repo.Topics, " ")}, " ")
		texts = append(texts, text)
		result.Projects = append(result.Projects, Project{
			Name:        repo.Name,
			URL:         repo.HTMLURL,
			Description: strings.TrimSpace(repo.Description),
			Language:    repo.Language,
			Topics:      repo.Topics,
			Stars:       repo.Stars,
			Forks:       repo.Forks,
			UpdatedAt:   repo.UpdatedAt,
		})
	}
	result.Repositories = len(repos)
	result.Languages = languageStats(languageCounts)
	result.Skills = topSkills(skillCounts, texts, 20)
	result.Score = projectScore(result, repos)
	result.ProfessionMatch, result.ProfessionMatched = professionMatch(profession, result.Skills, texts)
	result.Summary = summary(result, profession)
	sort.Slice(result.Projects, func(i, j int) bool {
		if result.Projects[i].Stars == result.Projects[j].Stars {
			return result.Projects[i].UpdatedAt > result.Projects[j].UpdatedAt
		}
		return result.Projects[i].Stars > result.Projects[j].Stars
	})
	if len(result.Projects) > 12 {
		result.Projects = result.Projects[:12]
	}
	if result.OriginalRepos == 0 {
		result.Warnings = append(result.Warnings, "Не найдено публичных нефорковых репозиториев")
	}
	if len(repos) == 100 {
		result.Warnings = append(result.Warnings, "GitHub вернул первые 100 репозиториев; профиль может быть неполным")
	}
	return result
}

func languageStats(counts map[string]int) []Language {
	out := make([]Language, 0, len(counts))
	for name, count := range counts {
		out = append(out, Language{Name: name, Repositories: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repositories == out[j].Repositories {
			return out[i].Name < out[j].Name
		}
		return out[i].Repositories > out[j].Repositories
	})
	return out
}

func topSkills(counts map[string]int, texts []string, limit int) []string {
	catalog := analysis.NewTaxonomyCatalog(nil, analysis.DefaultTaxonomy())
	extracted := analysis.ExtractSkills(texts, catalog)
	for skill, count := range extracted.Freq {
		counts[canonicalSkill(skill)] += count
	}
	type pair struct {
		name  string
		count int
	}
	pairs := make([]pair, 0, len(counts))
	for name, count := range counts {
		if name != "" {
			pairs = append(pairs, pair{name: name, count: count})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count == pairs[j].count {
			return pairs[i].name < pairs[j].name
		}
		return pairs[i].count > pairs[j].count
	})
	if len(pairs) > limit {
		pairs = pairs[:limit]
	}
	out := make([]string, 0, len(pairs))
	for _, item := range pairs {
		out = append(out, item.name)
	}
	return out
}

func canonicalSkill(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	known := map[string]string{
		"golang": "Go", "go": "Go", "js": "JavaScript", "javascript": "JavaScript",
		"ts": "TypeScript", "typescript": "TypeScript", "py": "Python", "python": "Python",
		"postgres": "PostgreSQL", "postgresql": "PostgreSQL", "k8s": "Kubernetes",
		"kubernetes": "Kubernetes", "docker": "Docker", "react": "React",
		"vue": "Vue", "node": "Node.js", "nodejs": "Node.js", "java": "Java",
		"css": "CSS", "html": "HTML", "scss": "SCSS", "sql": "SQL", "bash": "Bash",
		"shell": "Shell", "php": "PHP", "ruby": "Ruby", "rust": "Rust", "c++": "C++",
	}
	if normalized, ok := known[strings.ToLower(value)]; ok {
		return normalized
	}
	words := strings.Fields(value)
	for i, word := range words {
		runes := []rune(word)
		words[i] = strings.ToUpper(string(runes[0])) + strings.ToLower(string(runes[1:]))
	}
	return strings.Join(words, " ")
}

func projectScore(result Assessment, repos []githubRepo) int {
	score := 0.0
	score += math.Min(30, float64(result.OriginalRepos)*3)
	if result.TotalStars > 0 {
		score += math.Min(25, math.Log10(float64(result.TotalStars)+1)*10)
	}
	described := 0
	withTopics := 0
	recent := 0
	cutoff := time.Now().AddDate(-1, 0, 0)
	for _, repo := range repos {
		if repo.Fork {
			continue
		}
		if strings.TrimSpace(repo.Description) != "" {
			described++
		}
		if len(repo.Topics) > 0 {
			withTopics++
		}
		if updated, err := time.Parse(time.RFC3339, repo.UpdatedAt); err == nil && updated.After(cutoff) {
			recent++
		}
	}
	if result.OriginalRepos > 0 {
		score += 15 * float64(described) / float64(result.OriginalRepos)
		score += 10 * float64(withTopics) / float64(result.OriginalRepos)
		score += 15 * float64(recent) / float64(result.OriginalRepos)
	}
	score += math.Min(5, float64(len(result.Languages))*1.5)
	return int(math.Round(math.Min(100, score)))
}

func professionMatch(profession string, skills, texts []string) (int, bool) {
	target := profileTokens(profession)
	if len(target) == 0 {
		return 0, false
	}
	haystack := strings.ToLower(strings.Join(skills, " ") + " " + strings.Join(texts, " "))
	matched := 0
	for token := range target {
		if strings.Contains(haystack, token) {
			matched++
		}
	}
	if matched == 0 {
		return 0, false
	}
	return int(math.Round(math.Min(100, 100*float64(matched)/float64(len(target))))), true
}

func profileTokens(value string) map[string]bool {
	words := strings.FieldsFunc(strings.ToLower(strings.ReplaceAll(value, "ё", "е")), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= 'а' && r <= 'я') && !(r >= '0' && r <= '9')
	})
	out := make(map[string]bool)
	for _, word := range words {
		if len([]rune(word)) >= 3 {
			out[word] = true
		}
	}
	return out
}

func summary(result Assessment, profession string) string {
	parts := []string{fmt.Sprintf("Публичных репозиториев: %d, звёзд: %d.", result.OriginalRepos, result.TotalStars)}
	if len(result.Languages) > 0 {
		parts = append(parts, fmt.Sprintf("Основные языки: %s.", languageList(result.Languages, 4)))
	} else {
		parts = append(parts, "Языки в проектах не указаны.")
	}
	if strings.TrimSpace(profession) != "" {
		if result.ProfessionMatched {
			parts = append(parts, fmt.Sprintf("Совпадение с профессией «%s»: %d%%.", strings.TrimSpace(profession), result.ProfessionMatch))
		} else {
			parts = append(parts, fmt.Sprintf("В проектах не найдено явных признаков профессии «%s».", strings.TrimSpace(profession)))
		}
	}
	if result.Score >= 70 {
		parts = append(parts, "Профиль выглядит зрелым: есть описание, темы и недавняя активность.")
	} else if result.Score >= 40 {
		parts = append(parts, "Профиль частично заполнен: часть проектов без описания или без недавней активности.")
	} else {
		parts = append(parts, "Профиль требует ручной проверки: мало проектов или слабое оформление.")
	}
	return strings.Join(parts, " ")
}

func languageList(languages []Language, limit int) string {
	if len(languages) > limit {
		languages = languages[:limit]
	}
	names := make([]string, 0, len(languages))
	for _, language := range languages {
		names = append(names, language.Name)
	}
	return strings.Join(names, ", ")
}

// ExcludeProjects removes explicitly excluded repositories from the profile.
// It is used when the respondent edits the public GitHub data before an
// interview; the aggregate counters and languages are recalculated.
func ExcludeProjects(assessment Assessment, excluded []string) Assessment {
	skip := make(map[string]bool, len(excluded))
	for _, name := range excluded {
		skip[strings.ToLower(strings.TrimSpace(name))] = true
	}
	if len(skip) == 0 {
		return assessment
	}
	projects := make([]Project, 0, len(assessment.Projects))
	languageCounts := make(map[string]int)
	for _, project := range assessment.Projects {
		if skip[strings.ToLower(project.Name)] {
			continue
		}
		projects = append(projects, project)
		if project.Language != "" {
			languageCounts[canonicalSkill(project.Language)]++
		}
	}
	if len(projects) == 0 {
		assessment.Projects = nil
		assessment.Languages = nil
		assessment.OriginalRepos = 0
		assessment.Repositories = 0
		assessment.TotalStars = 0
		assessment.TotalForks = 0
		assessment.Skills = nil
		return assessment
	}
	assessment.Projects = projects
	assessment.Languages = languageStats(languageCounts)
	assessment.TotalStars = 0
	assessment.TotalForks = 0
	for _, project := range projects {
		assessment.TotalStars += project.Stars
		assessment.TotalForks += project.Forks
	}
	assessment.OriginalRepos = len(projects)
	return assessment
}

// deliberately contains facts and project names, not model instructions or
// interview answers.
func Context(assessment Assessment) string {
	parts := []string{
		fmt.Sprintf("GitHub @%s: публичных проектов %d, звёзд %d.", assessment.Username, assessment.OriginalRepos, assessment.TotalStars),
	}
	if len(assessment.Languages) > 0 {
		parts = append(parts, "Языки: "+languageList(assessment.Languages, 6)+".")
	}
	if len(assessment.Skills) > 0 {
		skills := assessment.Skills
		if len(skills) > 12 {
			skills = skills[:12]
		}
		parts = append(parts, "Навыки и темы: "+strings.Join(skills, ", ")+".")
	}
	projects := make([]string, 0, 5)
	for _, project := range assessment.Projects {
		if len(projects) >= 5 {
			break
		}
		description := strings.TrimSpace(project.Description)
		if len([]rune(description)) > 160 {
			description = string([]rune(description)[:160]) + "..."
		}
		line := project.Name
		if project.Language != "" {
			line += " (" + project.Language + ")"
		}
		if description != "" {
			line += ": " + description
		}
		projects = append(projects, line)
	}
	if len(projects) > 0 {
		parts = append(parts, "Проекты: "+strings.Join(projects, "; ")+".")
	}
	if assessment.ProfessionMatched {
		parts = append(parts, fmt.Sprintf("Совпадение с профессией: %d%%.", assessment.ProfessionMatch))
	}
	return strings.Join(parts, " ")
}
