// Команда seed-demo-users создаёт демонстрационные учётные записи из README:
// администратора, специалиста и обычного пользователя. Команда идемпотентна:
// повторный запуск обновляет роли и пароли, а не падает на дублях.
//
// Это инструмент локальной демонстрации, а не production-миграция: пароли
// фиксированные и совпадают с README. В рабочей среде запускать не нужно.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"activity-descriptor/internal/auth"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type demoUser struct {
	Email       string
	DisplayName string
	Password    string
	Role        auth.Role
}

type reportRow struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

// demoUsers — фиксированный набор учётных записей из README. Пароли те же, что
// в документации для локальной демонстрации.
func demoUsers() []demoUser {
	return []demoUser{
		{Email: "admin@modeus.local", DisplayName: "Администратор", Password: "Admin12345", Role: auth.RoleAdmin},
		{Email: "specialist@modeus.local", DisplayName: "Специалист", Password: "User12345", Role: auth.RoleSpecialist},
		{Email: "user@modeus.local", DisplayName: "Пользователь", Password: "User12345", Role: auth.RoleUser},
	}
}

// seedDemoUser создаёт или обновляет демо-учётку вместе с согласием на
// обработку персональных данных. Демо-согласие помечается как унаследованное
// (v1-legacy): пользователь его не давал, оно нужно, чтобы демо-сценарии
// работали без отдельного экрана согласия.
func seedDemoUser(ctx context.Context, pool *pgxpool.Pool, user demoUser) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("пароль %s: %w", user.Email, err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("транзакция для %s: %w", user.Email, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `INSERT INTO users(email, display_name, role_id, password_hash, consent_version, consent_at)
SELECT $1, $2, r.id, $3, 'v1', now() FROM roles r WHERE r.code = $4
ON CONFLICT (email) DO UPDATE
SET display_name = EXCLUDED.display_name,
    role_id = EXCLUDED.role_id,
    password_hash = EXCLUDED.password_hash,
    consent_version = 'v1',
    consent_at = COALESCE(users.consent_at, EXCLUDED.consent_at),
    updated_at = now()`,
		user.Email, user.DisplayName, string(hash), string(user.Role))
	if err != nil {
		return fmt.Errorf("сохранить %s: %w", user.Email, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("роль %q не найдена в таблице roles", user.Role)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_consents (user_id, document_version, accepted_at)
SELECT u.id, 'v1-legacy', u.consent_at
FROM users u
WHERE u.email = $1
  AND NOT EXISTS (
      SELECT 1 FROM user_consents c
      WHERE c.user_id = u.id AND c.document_version = 'v1-legacy'
  )`, user.Email); err != nil {
		return fmt.Errorf("согласие %s: %w", user.Email, err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("сохранить %s: %w", user.Email, err)
	}
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("seed-demo-users", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dsn := fs.String("dsn", "", "PostgreSQL DSN; по умолчанию берётся DATABASE_URL")
	asJSON := fs.Bool("json", false, "вывести результат в JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("лишние аргументы: %v", fs.Args())
	}

	url := strings.TrimSpace(*dsn)
	if url == "" {
		url = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	if url == "" {
		return errors.New("нужен DSN: задайте -dsn или DATABASE_URL")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return fmt.Errorf("подключение к PostgreSQL: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("нет подключения к PostgreSQL: %w", err)
	}

	rows := make([]reportRow, 0, len(demoUsers()))
	for _, user := range demoUsers() {
		if err := seedDemoUser(ctx, pool, user); err != nil {
			return err
		}
		rows = append(rows, reportRow{Email: user.Email, DisplayName: user.DisplayName, Role: string(user.Role)})
	}

	if *asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(rows)
	}
	for _, row := range rows {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", row.Role, row.Email, row.DisplayName)
	}
	fmt.Fprintf(stdout, "готово: %d демонстрационных учётных записи\n", len(rows))
	return nil
}
