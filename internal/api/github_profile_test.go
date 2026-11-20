package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"activity-descriptor/internal/githubprofile"
)

type fakeGitHubEvaluator struct {
	assessment githubprofile.Assessment
	err        error
}

func (f *fakeGitHubEvaluator) Evaluate(context.Context, string, string) (githubprofile.Assessment, error) {
	return f.assessment, f.err
}

func TestEvaluateGitHubHandler(t *testing.T) {
	server := NewServer(newMemStore())
	server.SetGitHubEvaluator(&fakeGitHubEvaluator{assessment: githubprofile.Assessment{
		Username: "octocat", Score: 72, ProfessionMatch: 80, ProfessionMatched: true,
	}})
	recorder := doMentorHandler(t, server.handleEvaluateGitHub, http.MethodPost,
		"/api/v1/github/evaluate", "user", `{"username":"octocat","profession":"Backend-разработчик"}`, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", recorder.Code, recorder.Body.String())
	}
}

func TestEvaluateGitHubHandlerErrors(t *testing.T) {
	server := NewServer(newMemStore())
	server.SetGitHubEvaluator(&fakeGitHubEvaluator{err: githubprofile.ErrNotFound})
	missing := doMentorHandler(t, server.handleEvaluateGitHub, http.MethodPost,
		"/api/v1/github/evaluate", "admin", `{"username":"missing"}`, nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d", missing.Code)
	}

	server.SetGitHubEvaluator(&fakeGitHubEvaluator{err: githubprofile.ErrRateLimited})
	limited := doMentorHandler(t, server.handleEvaluateGitHub, http.MethodPost,
		"/api/v1/github/evaluate", "admin", `{"username":"octocat"}`, nil)
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("limited status = %d", limited.Code)
	}

	server.SetGitHubEvaluator(&fakeGitHubEvaluator{err: errors.New("network down")})
	bad := doMentorHandler(t, server.handleEvaluateGitHub, http.MethodPost,
		"/api/v1/github/evaluate", "admin", `{"username":"octocat"}`, nil)
	if bad.Code != http.StatusBadGateway {
		t.Fatalf("unavailable status = %d", bad.Code)
	}

	invalid := doMentorHandler(t, server.handleEvaluateGitHub, http.MethodPost,
		"/api/v1/github/evaluate", "admin", `{"username":""}`, nil)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("empty username status = %d", invalid.Code)
	}
}

func TestEvaluateGitHubRequiresAuthentication(t *testing.T) {
	server := NewServer(newMemStore())
	server.SetGitHubEvaluator(&fakeGitHubEvaluator{})
	recorder := doMentorHandler(t, server.handleEvaluateGitHub, http.MethodPost,
		"/api/v1/github/evaluate", "", `{"username":"octocat"}`, nil)
	if recorder.Code != http.StatusUnauthorized && recorder.Code != http.StatusForbidden {
		t.Fatalf("anonymous status = %d", recorder.Code)
	}
}
