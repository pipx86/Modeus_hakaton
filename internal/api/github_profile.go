package api

import (
	"context"

	"activity-descriptor/internal/githubprofile"
)

type githubEvaluator interface {
	Evaluate(ctx context.Context, username, profession string) (githubprofile.Assessment, error)
}

// SetGitHubEvaluator wires the public GitHub profile reader used by the
// interview flow. It is optional so the in-memory demo server and tests can run
// without network access.
func (s *Server) SetGitHubEvaluator(evaluator githubEvaluator) {
	s.github = evaluator
}
