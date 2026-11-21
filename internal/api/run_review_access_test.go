package api

import (
	"net/http"
	"testing"

	"activity-descriptor/internal/domain"
)

func TestGrantReviewAccessRequiresOwner(t *testing.T) {
	store := newMemStore()
	store.seedRun(domain.Run{ID: "run-1", CreatedBy: "another-user"})
	handler := NewServer(store).Routes()
	response := do(t, handler, http.MethodPost, "/api/v1/runs/run-1/review-access", "user", `{"email":"reviewer@example.com"}`)
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-owner: %d %s", response.Code, response.Body.String())
	}
}
