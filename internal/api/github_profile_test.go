package api

import (
	"context"

	"activity-descriptor/internal/githubprofile"
)

type fakeGitHubEvaluator struct {
	assessment githubprofile.Assessment
	err        error
}

func (f *fakeGitHubEvaluator) Evaluate(context.Context, string, string) (githubprofile.Assessment, error) {
	return f.assessment, f.err
}
