package auth

import (
	"errors"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Role string

const (
	RoleAdmin   Role = "admin"
	RoleTeacher Role = "teacher"
	RoleROP     Role = "rop"
	RoleStudent Role = "student"
)

func ValidRole(s string) bool {
	switch Role(s) {
	case RoleAdmin, RoleTeacher, RoleROP, RoleStudent:
		return true
	}
	return false
}

type Claims struct {
	Sub  string `json:"sub"`
	Role string `json:"role"`
	jwt.RegisteredClaims
}

var signingKey []byte

func init() {
	s := os.Getenv("JWT_SECRET")
	if s == "" {
		signingKey = nil
	} else {
		signingKey = []byte(s)
	}
}

func SetSecretForTests(secret string) {
	signingKey = []byte(secret)
}

func Issue(sub string, role Role, ttl time.Duration) (string, error) {
	if signingKey == nil {
		return "", errors.New("JWT_SECRET is not set")
	}
	if !ValidRole(string(role)) {
		return "", errors.New("unknown role")
	}
	now := time.Now()
	c := &Claims{
		Sub:  sub,
		Role: string(role),
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			Subject:   sub,
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return tok.SignedString(signingKey)
}

func Parse(tokenStr string) (*Claims, error) {
	if signingKey == nil {
		return nil, errors.New("JWT_SECRET is not set")
	}
	parsed, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return signingKey, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, errors.New("invalid token")
	}
	if !ValidRole(claims.Role) {
		return nil, errors.New("unknown role in token")
	}
	return claims, nil
}
