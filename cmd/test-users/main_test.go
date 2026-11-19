package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"activity-descriptor/internal/auth"
)

func TestRunJSONOutputsAdminAndUser(t *testing.T) {
	auth.SetSecretForTests("test-secret-for-ci")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"-json"}, &stdout, &stderr); err != nil {
		t.Fatalf("run -json: %v (stderr: %s)", err, stderr.String())
	}

	var got report
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("вывод -json не разобрался: %v\n%s", err, stdout.String())
	}
	if got.TTL != "24h0m0s" {
		t.Fatalf("ожидали ttl 24h0m0s, получили %q", got.TTL)
	}
	if len(got.Users) != 2 {
		t.Fatalf("ожидали двух пользователей, получили %d: %+v", len(got.Users), got.Users)
	}

	want := []struct {
		subject  string
		role     string
		readOnly bool
	}{
		{subject: "demo-admin", role: "admin"},
		{subject: "demo-user", role: "user"},
	}
	for i, expected := range want {
		user := got.Users[i]
		if user.Subject != expected.subject || user.Role != expected.role || user.ReadOnly != expected.readOnly {
			t.Fatalf("пользователь %d: ожидали %+v, получили %+v", i, expected, user)
		}
		claims, err := auth.Parse(user.Token)
		if err != nil {
			t.Fatalf("%s: токен не разобрался: %v", user.Subject, err)
		}
		if claims.Sub != expected.subject || claims.Role != expected.role {
			t.Fatalf("%s: неверные claims: %+v", user.Subject, claims)
		}
	}

	if strings.Contains(stdout.String(), "test-secret-for-ci") {
		t.Fatal("секрет подписи не должен попадать в вывод")
	}
}

func TestRunHumanOutputHasViteAPITokenAndRoles(t *testing.T) {
	auth.SetSecretForTests("test-secret-for-ci")

	var stdout, stderr bytes.Buffer
	if err := run(nil, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v (stderr: %s)", err, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"subject=demo-admin",
		"subject=demo-user",
		"role=admin",
		"role=user",
		"VITE_API_TOKEN",
		"$env:VITE_API_TOKEN",
		"export VITE_API_TOKEN",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("в человекочитаемом выводе нет %q:\n%s", want, out)
		}
	}
	if count := strings.Count(out, "$env:VITE_API_TOKEN = "); count != 2 {
		t.Fatalf("ожидали две PowerShell-строки для двух пользователей, получили %d", count)
	}
	if count := strings.Count(out, "export VITE_API_TOKEN="); count != 2 {
		t.Fatalf("ожидали две bash/zsh-строки для двух пользователей, получили %d", count)
	}
}

func TestRunRejectsInvalidTTL(t *testing.T) {
	auth.SetSecretForTests("test-secret-for-ci")
	for _, args := range [][]string{
		{"-ttl=25h"},
		{"-ttl=0"},
		{"-ttl=-1h"},
		{"-ttl=1s", "лишний-аргумент"},
	} {
		var stdout, stderr bytes.Buffer
		if err := run(args, &stdout, &stderr); err == nil {
			t.Fatalf("run %v должен вернуть ошибку", args)
		}
	}
}
