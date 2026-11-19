package auth

import (
	"context"
	"net/http"
	"strings"
)

type roleKey struct{}
type subjectKey struct{}

func WithRole(ctx context.Context, role Role) context.Context {
	return context.WithValue(ctx, roleKey{}, NormalizeRole(role))
}

func FromContext(ctx context.Context) (Role, bool) {
	r, ok := ctx.Value(roleKey{}).(Role)
	return r, ok
}

// Subject возвращает идентификатор пользователя из проверенного токена.
func Subject(ctx context.Context) string { value, _ := ctx.Value(subjectKey{}).(string); return value }

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var tokenStr string

		h := r.Header.Get("Authorization")
		if strings.HasPrefix(h, "Bearer ") {
			tokenStr = strings.TrimSpace(h[len("Bearer "):])
		} else if q := r.URL.Query().Get("access_token"); q != "" {
			tokenStr = q
		}

		if tokenStr == "" {
			writeUnauthorized(w)
			return
		}

		claims, err := Parse(tokenStr)
		if err != nil {
			writeUnauthorized(w)
			return
		}

		ctx := WithRole(r.Context(), Role(claims.Role))
		ctx = context.WithValue(ctx, subjectKey{}, claims.Sub)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"code":"UNAUTHORIZED","message":"missing or invalid token"}`))
}

func MiddlewareWithWhitelist(next http.Handler, whitelistPaths []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, path := range whitelistPaths {
			if strings.HasPrefix(r.URL.Path, path) {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Если не в белом списке — обычная проверка токена
		var tokenStr string

		h := r.Header.Get("Authorization")
		if strings.HasPrefix(h, "Bearer ") {
			tokenStr = strings.TrimSpace(h[len("Bearer "):])
		} else if q := r.URL.Query().Get("access_token"); q != "" {
			tokenStr = q
		}

		if tokenStr == "" {
			writeUnauthorized(w)
			return
		}

		claims, err := Parse(tokenStr)
		if err != nil {
			writeUnauthorized(w)
			return
		}

		ctx := WithRole(r.Context(), Role(claims.Role))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
