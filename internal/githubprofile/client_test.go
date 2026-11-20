package githubprofile

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEvaluateGitHubProfile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/users/octocat", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login":"octocat","name":"Octo Cat","html_url":"https://github.com/octocat","avatar_url":"https://avatar"}`))
	})
	mux.HandleFunc("/users/octocat/repos", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"name":"backend-service","html_url":"https://github.com/octocat/backend-service","description":"Go and PostgreSQL service","language":"Go","topics":["docker","postgresql"],"stargazers_count":42,"forks_count":3,"fork":false,"updated_at":"2026-11-01T00:00:00Z"},
			{"name":"frontend","html_url":"https://github.com/octocat/frontend","description":"React dashboard","language":"TypeScript","topics":["react"],"stargazers_count":8,"forks_count":1,"fork":false,"updated_at":"2026-10-01T00:00:00Z"},
			{"name":"forked","html_url":"https://github.com/octocat/forked","language":"JavaScript","fork":true,"updated_at":"2026-01-01T00:00:00Z"}
		]`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := &Client{HTTP: server.Client(), BaseURL: server.URL, Token: "test-token"}
	assessment, err := client.Evaluate(context.Background(), "octocat", "Backend-разработчик")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if assessment.OriginalRepos != 2 || assessment.TotalStars != 50 {
		t.Fatalf("repos=%d stars=%d", assessment.OriginalRepos, assessment.TotalStars)
	}
	if assessment.Score <= 0 || assessment.ProfessionMatch <= 0 || !assessment.ProfessionMatched {
		t.Fatalf("assessment = %+v", assessment)
	}
	if len(assessment.Projects) != 2 {
		t.Fatalf("projects = %d", len(assessment.Projects))
	}
	if assessment.Summary == "" || len(assessment.Languages) == 0 || len(assessment.Skills) == 0 {
		t.Fatalf("incomplete assessment: %+v", assessment)
	}
}

func TestExcludeProjectsRecalculatesProfile(t *testing.T) {
	assessment := Assessment{
		Username:      "octocat",
		OriginalRepos: 2,
		TotalStars:    50,
		Languages:     []Language{{Name: "Go", Repositories: 1}, {Name: "TypeScript", Repositories: 1}},
		Projects: []Project{
			{Name: "backend", Language: "Go", Stars: 42},
			{Name: "frontend", Language: "TypeScript", Stars: 8},
		},
	}
	filtered := ExcludeProjects(assessment, []string{"backend"})
	if filtered.OriginalRepos != 1 || filtered.TotalStars != 8 {
		t.Fatalf("filtered = %+v", filtered)
	}
	if len(filtered.Projects) != 1 || filtered.Projects[0].Name != "frontend" {
		t.Fatalf("projects = %+v", filtered.Projects)
	}
	if len(filtered.Languages) != 1 || filtered.Languages[0].Name != "TypeScript" {
		t.Fatalf("languages = %+v", filtered.Languages)
	}
}

func TestEvaluateGitHubProfileErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/missing" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "rate limit", http.StatusForbidden)
	}))
	defer server.Close()
	client := &Client{HTTP: server.Client(), BaseURL: server.URL}
	if _, err := client.Evaluate(context.Background(), "missing", ""); err != ErrNotFound {
		t.Fatalf("missing error = %v", err)
	}
	if _, err := client.Evaluate(context.Background(), "limited", ""); err != ErrRateLimited {
		t.Fatalf("limited error = %v", err)
	}
	if _, err := client.Evaluate(context.Background(), "bad name", ""); err == nil {
		t.Fatal("invalid username accepted")
	}
}
