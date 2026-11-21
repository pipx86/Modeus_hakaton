package api

import (
	"net/http"
	"testing"

	"activity-descriptor/internal/domain"
)

const interviewAccessRunID = "55555555-5555-5555-5555-555555555555"

// TestInterviewEvidenceRequiresRunAccess guards the interview evidence
// endpoints: a non-owner must not be able to post an attachment or a link into
// someone else's active interview session, while an admin still can.
func TestInterviewEvidenceRequiresRunAccess(t *testing.T) {
	m := newMemStore()
	m.seedRun(domain.Run{
		ID:         interviewAccessRunID,
		Status:     domain.RunRunning,
		Profession: "cook",
		CreatedBy:  "interview-owner",
	})
	h := NewServerWithChat(m, guestChat{}).Routes()

	attachmentPath := "/api/v1/runs/" + interviewAccessRunID + "/attachments"
	linkPath := "/api/v1/runs/" + interviewAccessRunID + "/attachments/link"
	linkBody := `{"url":"https://example.com/commit/abc","title":"commit"}`

	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{"attachment", attachmentPath, ""},
		{"link", linkPath, linkBody},
	} {
		t.Run("user forbidden "+tc.name, func(t *testing.T) {
			rr := do(t, h, http.MethodPost, tc.path, "user", tc.body)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("user POST %s: status = %d, want 403 (%s)", tc.path, rr.Code, rr.Body.String())
			}
		})
		t.Run("admin allowed "+tc.name, func(t *testing.T) {
			rr := do(t, h, http.MethodPost, tc.path, "admin", tc.body)
			if rr.Code == http.StatusForbidden || rr.Code == http.StatusUnauthorized {
				t.Fatalf("admin POST %s: status = %d, want not 403/401 (%s)", tc.path, rr.Code, rr.Body.String())
			}
		})
	}
}
