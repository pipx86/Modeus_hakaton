package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// 1. Пустой токен после Bearer
func TestMiddlewareRejectsEmptyBearerToken(t *testing.T) {
	SetSecretForTests("test-secret")
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer ") // Пустое значение
	rr := httptest.NewRecorder()
	Middleware(echoHandler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("ожидали 401 на пустой Bearer, получили %d", rr.Code)
	}
}

// 2. Некорректный формат Claims (роль не строка)
// Исправлено: теперь ожидаем ошибку JSON-распаковки, которая возникает раньше проверки роли
func TestParseRejectsNonStringRole(t *testing.T) {
	SetSecretForTests("test-secret")

	// Создаем токен вручную через MapClaims, где role - это число
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  "u-bad",
		"role": 123, // Не строка!
		"exp":  time.Now().Add(time.Hour).Unix(),
	})
	tokStr, _ := token.SignedString([]byte("test-secret"))

	_, err := Parse(tokStr)
	if err == nil {
		t.Fatal("токен с числовой ролью должен быть отклонен")
	}

	// Ожидаем ошибку JSON unmarshal или invalid token
	errMsg := err.Error()
	isJsonError := strings.Contains(errMsg, "cannot unmarshal number") ||
		strings.Contains(errMsg, "token is malformed") ||
		strings.Contains(errMsg, "invalid")

	if !isJsonError {
		t.Fatalf("ожидали ошибку типа 'cannot unmarshal' или 'invalid', получили: %v", err)
	}
}

// 3. Алгоритм None (критическая уязвимость)
func TestParseRejectsNoneAlgorithm(t *testing.T) {
	SetSecretForTests("test-secret")
	token := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"sub":  "hacker",
		"role": "admin",
		"exp":  time.Now().Add(time.Hour).Unix(),
	})
	// Подписываем пустой строкой (как требует none)
	tokStr, _ := token.SignedString(jwt.UnsafeAllowNoneSignatureType)

	_, err := Parse(tokStr)
	if err == nil {
		t.Fatal("токен с алгоритмом none должен быть отклонен")
	}
	if !strings.Contains(err.Error(), "unexpected signing method") {
		t.Fatalf("ожидали ошибку метода подписи, получили: %v", err)
	}
}

// 4. Пустой Subject (технически валидно, проверяем что не падает)
func TestIssueAllowsEmptySubject(t *testing.T) {
	SetSecretForTests("test-secret")
	tok, err := Issue("", RoleAdmin, time.Hour)
	if err != nil {
		t.Fatalf("Issue не должен падать на пустом sub: %v", err)
	}
	claims, err := Parse(tok)
	if err != nil || claims.Sub != "" {
		t.Fatalf("пустой sub должен сохраниться: %v, %+v", err, claims)
	}
}

// 5. Приоритет заголовка над query
func TestMiddlewareHeaderPriorityOverQuery(t *testing.T) {
	SetSecretForTests("test-secret")
	validTok, _ := Issue("u-header", RoleTeacher, time.Hour)
	invalidTok, _ := Issue("u-query", RoleStudent, -time.Second) // Просрочен

	req := httptest.NewRequest("GET", "/?access_token="+invalidTok, nil)
	req.Header.Set("Authorization", "Bearer "+validTok)

	rr := httptest.NewRecorder()
	Middleware(echoHandler()).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("заголовок должен иметь приоритет, получили %d", rr.Code)
	}
	if rr.Body.String() != "teacher" {
		t.Fatalf("должна быть роль из заголовка (teacher), получила %q", rr.Body.String())
	}
}

// 6. Токен с TTL = 0 (истекает сразу)
func TestParseRejectsZeroTTL(t *testing.T) {
	SetSecretForTests("test-secret")
	tok, _ := Issue("u-zero", RoleStudent, 0)
	time.Sleep(10 * time.Millisecond)

	_, err := Parse(tok)
	if err == nil {
		t.Fatal("токен с нулевым TTL должен быть просрочен")
	}
}
