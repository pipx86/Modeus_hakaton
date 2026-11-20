package api

import (
	"context"
	"net/http"
	"testing"
)

func TestUpdateUserRoleHandler(t *testing.T) {
	store := newMemStore()
	user, err := store.CreateUser(context.Background(), "specialist@example.com", "Специалист", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(store)

	recorder := doMentorHandler(t, server.handleUpdateUserRole, http.MethodPatch,
		"/api/v1/users/"+user.ID+"/role", "admin", `{"role":"specialist"}`, map[string]string{"id": user.ID})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update role: got %d (%s)", recorder.Code, recorder.Body.String())
	}
	updated, err := store.UserByEmail(context.Background(), "specialist@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Role != "specialist" {
		t.Fatalf("role = %q, want specialist", updated.Role)
	}
}

func TestUpdateUserRoleHandlerValidation(t *testing.T) {
	store := newMemStore()
	user, err := store.CreateUser(context.Background(), "user@example.com", "Пользователь", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(store)

	forbidden := doMentorHandler(t, server.handleUpdateUserRole, http.MethodPatch,
		"/api/v1/users/"+user.ID+"/role", "user", `{"role":"admin"}`, map[string]string{"id": user.ID})
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("user changed role: got %d", forbidden.Code)
	}

	badRole := doMentorHandler(t, server.handleUpdateUserRole, http.MethodPatch,
		"/api/v1/users/"+user.ID+"/role", "admin", `{"role":"root"}`, map[string]string{"id": user.ID})
	if badRole.Code != http.StatusBadRequest {
		t.Fatalf("invalid role: got %d", badRole.Code)
	}

	missing := doMentorHandler(t, server.handleUpdateUserRole, http.MethodPatch,
		"/api/v1/users/user-missing/role", "admin", `{"role":"specialist"}`, map[string]string{"id": "user-missing"})
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing user: got %d", missing.Code)
	}
}
