package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// 1. Тест на ветку "insufficient permissions" в RequireRole
func TestGuardRejectsUnknownRoleWithMessage(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()

	handler := wrapWithRole(guardedHandler([]Role{RoleAdmin}), RoleTeacher)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("ожидали 403, получили %d", rr.Code)
	}

	body := rr.Body.String()
	if body != `{"code":"FORBIDDEN","message":"insufficient permissions for this action"}` {
		t.Fatalf("неверное сообщение об ошибке: %s", body)
	}
}

// 2. Тест на ветку "unknown role in token" в Parse
func TestParseRejectsValidTokenWithInvalidRole(t *testing.T) {
	SetSecretForTests("test-secret")

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  "u-hacker",
		"role": "superadmin",
		"exp":  time.Now().Add(time.Hour).Unix(),
	})
	tokStr, _ := token.SignedString([]byte("test-secret"))

	_, err := Parse(tokStr)
	if err == nil {
		t.Fatal("токен с недопустимой ролью должен быть отклонен")
	}
	if !strings.Contains(err.Error(), "unknown role in token") {
		t.Fatalf("ожидали ошибку 'unknown role in token', получили: %v", err)
	}
}

// 3. Тест на ветку "JWT_SECRET is not set" в Issue
func TestIssueFailsWithoutSecret(t *testing.T) {
	oldKey := signingKey
	signingKey = nil
	defer func() { signingKey = oldKey }()

	_, err := Issue("u-test", RoleAdmin, time.Hour)
	if err == nil {
		t.Fatal("Issue должен падать без установленного секрета")
	}
	if !strings.Contains(err.Error(), "JWT_SECRET is not set") {
		t.Fatalf("неверная ошибка: %v", err)
	}
}

// 4. Тест на ветку "JWT_SECRET is not set" в Parse
func TestParseFailsWithoutSecret(t *testing.T) {
	oldKey := signingKey
	signingKey = nil
	defer func() { signingKey = oldKey }()

	_, err := Parse("some.random.token")
	if err == nil {
		t.Fatal("Parse должен падать без установленного секрета")
	}
	if !strings.Contains(err.Error(), "JWT_SECRET is not set") {
		t.Fatalf("неверная ошибка: %v", err)
	}
}

// 5. Тест на MiddlewareWithWhitelist: путь НЕ в whitelist и нет токена
func TestMiddlewareWithWhitelistRejectsProtectedPath(t *testing.T) {
	SetSecretForTests("test-secret")

	h := MiddlewareWithWhitelist(echoHandler(), []string{"/public"})

	req := httptest.NewRequest("GET", "/protected", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("ожидали 401 для защищенного пути без токена, получили %d", rr.Code)
	}
}

// 6. Тест на MiddlewareWithWhitelist: путь В whitelist (проходит без токена)
// 6. Тест на MiddlewareWithWhitelist: путь В whitelist (проходит без токена)
func TestMiddlewareWithWhitelistAllowsPublicPath(t *testing.T) {
	SetSecretForTests("test-secret")

	// Используем простой хендлер, который не требует роль из контекста
	publicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	h := MiddlewareWithWhitelist(publicHandler, []string{"/public"})

	req := httptest.NewRequest("GET", "/public/docs", nil) // Путь начинается с /public
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("ожидали 200 для публичного пути, получили %d", rr.Code)
	}
}

// 7. Тест на утилиту Subject из контекста
func TestSubjectFromContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), subjectKey{}, "user-123")
	sub := Subject(ctx)
	if sub != "user-123" {
		t.Fatalf("ожидали 'user-123', получили '%s'", sub)
	}

	emptySub := Subject(context.Background())
	if emptySub != "" {
		t.Fatalf("ожидали пустую строку для пустого контекста, получили '%s'", emptySub)
	}
}

// 8. Тест на ValidRole с пустой строкой и мусором
func TestValidRoleEdgeCases(t *testing.T) {
	if ValidRole("") {
		t.Fatal("пустая строка не должна быть валидной ролью")
	}
	if ValidRole(" ") {
		t.Fatal("пробел не должен быть валидной ролью")
	}
	if ValidRole("ADMIN") { // Регистр имеет значение
		t.Fatal("ADMIN в верхнем регистре не должен быть валидной ролью")
	}
}
