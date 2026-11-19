package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"activity-descriptor/internal/auth"
)

func TestListUsersRequiresAdmin(t *testing.T) {
	_, ts := setupCRUDTest(t)
	auth.EnsureTestSecret()

	adminTok, _ := auth.Issue("demo-admin", auth.RoleAdmin, time.Hour)
	guestTok, _ := auth.Issue("demo-user", auth.RoleUser, time.Hour)

	t.Run("admin", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/users", nil)
		req.Header.Set("Authorization", "Bearer "+adminTok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("ожидали 200, получили %d", resp.StatusCode)
		}
		var users []authUserDTO
		if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(users) != 0 {
			t.Fatalf("ожидали пустой список пользователей, получили %d", len(users))
		}
	})

	t.Run("guest", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/users", nil)
		req.Header.Set("Authorization", "Bearer "+guestTok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("ожидали 403, получили %d", resp.StatusCode)
		}
	})
}
