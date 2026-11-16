package auth

import (
	"os"
	"time"
)

func EnsureTestSecret() {
	if os.Getenv("JWT_SECRET") == "" && signingKey == nil {
		SetSecretForTests("test-secret-for-ci")
	}
}

func TokenFor(sub string, role Role) string {
	EnsureTestSecret()
	tok, err := Issue(sub, role, time.Hour)
	if err != nil {
		panic("TokenFor: " + err.Error())
	}
	return tok
}
