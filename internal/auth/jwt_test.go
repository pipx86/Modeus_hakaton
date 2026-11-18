package auth

import (
	"strings"
	"testing"
	"time"
)

func TestIssueAndParseRoundTrip(t *testing.T) {
	SetSecretForTests("test-secret-for-ci")
	tok, err := Issue("u-42", RoleTeacher, time.Hour)
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}
	if strings.Count(tok, ".") != 2 {
		t.Fatalf("not a JWT: %s", tok)
	}
	claims, err := Parse(tok)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if claims.Sub != "u-42" || claims.Role != "teacher" {
		t.Fatalf("wrong claims: %+v", claims)
	}
}

func TestParseRejectsTamperedSignature(t *testing.T) {
	SetSecretForTests("test-secret-for-ci")
	tok, _ := Issue("u-1", RoleAdmin, time.Hour)
	bad := tok[:len(tok)-3] + "XYZ"
	if _, err := Parse(bad); err == nil {
		t.Fatalf("tampered token must be rejected")
	}
}

func TestParseRejectsExpired(t *testing.T) {
	SetSecretForTests("test-secret-for-ci")
	tok, _ := Issue("u-2", RoleStudent, -time.Second) // уже просрочен
	if _, err := Parse(tok); err == nil {
		t.Fatalf("expired token must be rejected")
	}
}

func TestIssueRejectsUnknownRole(t *testing.T) {
	SetSecretForTests("test-secret-for-ci")
	if _, err := Issue("u-3", Role("wizard"), time.Hour); err == nil {
		t.Fatalf("unknown role must be rejected at issue")
	}
}

func TestGuestRoleIsValidAndRoundTrips(t *testing.T) {
	SetSecretForTests("test-secret-for-ci")
	if !ValidRole(string(RoleGuest)) {
		t.Fatal("guest должен быть допустимой ролью")
	}
	tok, err := Issue("demo-guest", RoleGuest, time.Hour)
	if err != nil {
		t.Fatalf("не удалось выпустить guest-токен: %v", err)
	}
	claims, err := Parse(tok)
	if err != nil {
		t.Fatalf("guest-токен не разобрался: %v", err)
	}
	if claims.Sub != "demo-guest" || claims.Role != string(RoleGuest) {
		t.Fatalf("неверные claims: %+v", claims)
	}
}

func TestParseRejectsWrongSecret(t *testing.T) {
	SetSecretForTests("secret-A")
	tok, _ := Issue("u-4", RoleROP, time.Hour)
	SetSecretForTests("secret-B")
	if _, err := Parse(tok); err == nil {
		t.Fatalf("token signed with other secret must fail parse")
	}
}
