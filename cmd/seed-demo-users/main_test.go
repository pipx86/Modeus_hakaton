package main

import (
	"strings"
	"testing"

	"activity-descriptor/internal/auth"
)

func TestDemoUsersAreConsistent(t *testing.T) {
	users := demoUsers()
	if len(users) != 3 {
		t.Fatalf("ожидались три демо-учётки, получено %d", len(users))
	}
	seenEmails := map[string]bool{}
	seenRoles := map[auth.Role]bool{}
	for _, user := range users {
		if strings.TrimSpace(user.Email) == "" || !strings.Contains(user.Email, "@") {
			t.Fatalf("некорректный email: %q", user.Email)
		}
		if seenEmails[user.Email] {
			t.Fatalf("дубль email: %q", user.Email)
		}
		seenEmails[user.Email] = true
		if !auth.ValidRole(string(user.Role)) {
			t.Fatalf("недопустимая роль %q у %s", user.Role, user.Email)
		}
		seenRoles[user.Role] = true
		if len(user.Password) < 8 {
			t.Fatalf("пароль %s короче 8 символов", user.Email)
		}
	}
	for _, role := range []auth.Role{auth.RoleAdmin, auth.RoleSpecialist, auth.RoleUser} {
		if !seenRoles[role] {
			t.Fatalf("нет учётной записи с ролью %q", role)
		}
	}
}

func TestRunRequiresDSN(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	var out, errOut strings.Builder
	err := run(nil, &out, &errOut)
	if err == nil {
		t.Fatal("без DSN ожидалась ошибка")
	}
	if !strings.Contains(err.Error(), "DSN") {
		t.Fatalf("ошибка должна подсказывать про DSN: %v", err)
	}
}

func TestRunRejectsExtraArgs(t *testing.T) {
	var out, errOut strings.Builder
	if err := run([]string{"лишний"}, &out, &errOut); err == nil {
		t.Fatal("ожидалась ошибка на лишние аргументы")
	}
}
