package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"activity-descriptor/internal/auth"
)

func authRequest(t *testing.T, handler http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestRegisterLoginFlow(t *testing.T) {
	auth.EnsureTestSecret()
	server := NewServer(newMemStore())
	handler := server.Routes()

	admin := authRequest(t, handler, "/api/v1/auth/register",
		`{"email":"admin@example.com","display_name":"Админ","password":"password123"}`)
	if admin.Code != http.StatusCreated {
		t.Fatalf("первый register: %d %s", admin.Code, admin.Body.String())
	}
	var adminBody authResponse
	if err := json.Unmarshal(admin.Body.Bytes(), &adminBody); err != nil {
		t.Fatal(err)
	}
	if adminBody.User.Role != string(auth.RoleAdmin) || adminBody.Token == "" {
		t.Fatalf("первый пользователь должен быть admin: %+v", adminBody)
	}

	user := authRequest(t, handler, "/api/v1/auth/register",
		`{"email":"user@example.com","display_name":"Пользователь","password":"password123"}`)
	if user.Code != http.StatusCreated {
		t.Fatalf("второй register: %d %s", user.Code, user.Body.String())
	}
	var userBody authResponse
	if err := json.Unmarshal(user.Body.Bytes(), &userBody); err != nil {
		t.Fatal(err)
	}
	if userBody.User.Role != string(auth.RoleUser) {
		t.Fatalf("второй пользователь должен быть user: %+v", userBody)
	}

	login := authRequest(t, handler, "/api/v1/auth/login",
		`{"email":"user@example.com","password":"password123"}`)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	var loginBody authResponse
	if err := json.Unmarshal(login.Body.Bytes(), &loginBody); err != nil {
		t.Fatal(err)
	}
	claims, err := auth.Parse(loginBody.Token)
	if err != nil || claims.Role != string(auth.RoleUser) {
		t.Fatalf("login token: claims=%+v err=%v", claims, err)
	}

	wrong := authRequest(t, handler, "/api/v1/auth/login",
		`{"email":"user@example.com","password":"wrong-password"}`)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", wrong.Code)
	}
}

func TestRegisterValidationAndDuplicate(t *testing.T) {
	server := NewServer(newMemStore())
	handler := server.Routes()

	bad := authRequest(t, handler, "/api/v1/auth/register",
		`{"email":"not-an-email","display_name":"A","password":"short"}`)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("validation: %d %s", bad.Code, bad.Body.String())
	}

	first := authRequest(t, handler, "/api/v1/auth/register",
		`{"email":"dup@example.com","display_name":"Первый","password":"password123"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first: %d", first.Code)
	}
	second := authRequest(t, handler, "/api/v1/auth/register",
		`{"email":"dup@example.com","display_name":"Второй","password":"password123"}`)
	if second.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d %s", second.Code, second.Body.String())
	}
}
