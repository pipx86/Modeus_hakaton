package auth

import (
	"context"
	"net/http"
	"strings"
)

type roleKey struct{}

func WithRole(ctx context.Context, role Role) context.Context {
	return context.WithValue(ctx, roleKey{}, role)
}

func FromContext(ctx context.Context) (Role, bool) {
	r, ok := ctx.Value(roleKey{}).(Role)
	return r, ok
}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			writeUnauthorized(w)
			return
		}
		tokenStr := strings.TrimSpace(h[len("Bearer "):])
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

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"code":"UNAUTHORIZED","message":"missing or invalid token"}`))
}
