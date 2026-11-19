// Команда test-users выпускает JWT демонстрационных пользователей admin и user.
// Это токены нашего API для локальной демонстрации, а не пользователи hh.ru.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"activity-descriptor/internal/auth"
)

const (
	defaultTTL = 24 * time.Hour
	maxTTL     = 24 * time.Hour
)

type demoUser struct {
	Subject  string `json:"subject"`
	Role     string `json:"role"`
	Token    string `json:"token"`
	ReadOnly bool   `json:"read_only"`
}

type report struct {
	TTL   string     `json:"ttl"`
	Users []demoUser `json:"users"`
}

// demoRoles — фиксированный набор демонстрационных пользователей.
var demoRoles = []struct {
	subject  string
	role     auth.Role
	readOnly bool
}{
	{subject: "demo-admin", role: auth.RoleAdmin},
	{subject: "demo-user", role: auth.RoleUser},
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run выпускает оба токена и печатает их в stdout. Секрет подписи auth берёт
// из JWT_SECRET, поэтому база данных и дополнительные флаги не нужны.
func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("test-users", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ttl := fs.Duration("ttl", defaultTTL, "срок действия токенов, не более суток")
	asJSON := fs.Bool("json", false, "вывести JSON для автоматизации")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("лишние аргументы: %v", fs.Args())
	}
	if *ttl <= 0 || *ttl > maxTTL {
		return errors.New("ttl должен быть больше нуля и не более суток")
	}
	for _, item := range demoRoles {
		if !auth.ValidRole(string(item.role)) {
			return fmt.Errorf("недопустимая роль %q", item.role)
		}
	}

	result := report{TTL: ttl.String()}
	for _, item := range demoRoles {
		token, err := auth.Issue(item.subject, item.role, *ttl)
		if err != nil {
			return fmt.Errorf("%s: %w", item.subject, err)
		}
		result.Users = append(result.Users, demoUser{
			Subject:  item.subject,
			Role:     string(item.role),
			Token:    token,
			ReadOnly: item.readOnly,
		})
	}

	if *asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	return writeHuman(stdout, result)
}

// writeHuman печатает строки, готовые для копирования в PowerShell и bash/zsh.
func writeHuman(w io.Writer, r report) error {
	if _, err := fmt.Fprintf(w, "Демонстрационные JWT (ttl=%s); секрет подписи берётся из JWT_SECRET\n\n", r.TTL); err != nil {
		return err
	}
	for _, user := range r.Users {
		access := "полный доступ"
		if user.ReadOnly {
			access = "только чтение"
		}
		if _, err := fmt.Fprintf(w, "%s: subject=%s role=%s, %s\n", user.Role, user.Subject, user.Role, access); err != nil {
			return err
		}
		// Frontend читает токен из переменной VITE_API_TOKEN.
		if _, err := fmt.Fprintf(w, "  PowerShell: $env:VITE_API_TOKEN = '%s'\n", user.Token); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  bash/zsh:   export VITE_API_TOKEN='%s'\n\n", user.Token); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w, "user — обычный пользователь: создаёт исследования, проходит интервью и читает результаты.")
	return err
}
