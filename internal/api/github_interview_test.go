package api

import (
	"net/http"
	"strings"
	"testing"

	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/githubprofile"
)

type profileChatStub struct {
	profile string
	started bool
}

func (s *profileChatStub) Start(string, string) error {
	s.started = true
	return nil
}

func (s *profileChatStub) Messages(string) ([]chat.Message, error) { return nil, nil }

func (s *profileChatStub) Post(string, string) (chat.Message, error) { return chat.Message{}, nil }

func (s *profileChatStub) Subscribe(string) (<-chan chat.Event, func(), error) {
	ch := make(chan chat.Event)
	close(ch)
	return ch, func() {}, nil
}

func (s *profileChatStub) SetProfileContext(_ string, profile string) {
	s.profile = profile
}

func TestCreateChatRunAddsGitHubProfile(t *testing.T) {
	server := NewServer(newMemStore())
	chatStub := &profileChatStub{}
	server.chat = chatStub
	server.SetGitHubEvaluator(&fakeGitHubEvaluator{assessment: githubprofile.Assessment{
		Username: "octocat", OriginalRepos: 2, TotalStars: 10,
		Languages: []githubprofile.Language{{Name: "Go", Repositories: 2}},
		Skills:    []string{"Go", "PostgreSQL"},
	}})
	recorder := doMentorHandler(t, server.handleCreateRun, http.MethodPost, "/api/v1/runs", "user",
		`{"profession":"Backend-разработчик","type":"chat","github_username":"octocat"}`, nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d (%s)", recorder.Code, recorder.Body.String())
	}
	if !chatStub.started {
		t.Fatal("chat was not started")
	}
	if !strings.Contains(chatStub.profile, "octocat") || !strings.Contains(chatStub.profile, "Go") {
		t.Fatalf("profile context = %q", chatStub.profile)
	}
}
