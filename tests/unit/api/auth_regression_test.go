package api_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"activity-descriptor/internal/api"
	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConcurrentRegistrationsCreateOneAdmin(t *testing.T) {
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	schema := "auth_ga_" + strings.ToLower(rand.Text())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = pool.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := pool.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("удалить тестовую схему: %v", err)
		}
	}()
	cfg := pool.Config()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	isolated, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer isolated.Close()
	if _, err = isolated.Exec(ctx, `CREATE TABLE roles(id integer PRIMARY KEY, code text UNIQUE);
		CREATE TABLE users(id uuid PRIMARY KEY DEFAULT gen_random_uuid(), email text UNIQUE,
			display_name text NOT NULL, role_id integer REFERENCES roles, password_hash text NOT NULL);
		INSERT INTO roles(id,code) VALUES (1,'admin'),(2,'user');`); err != nil {
		t.Fatal(err)
	}
	store := api.NewPGStore(isolated)
	const registrations = 8
	type result struct {
		user *domain.User
		err  error
	}
	results := make(chan result, registrations)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for index := range registrations {
		workers.Go(func() {
			<-start
			user, err := store.RegisterUser(ctx, fmt.Sprintf("actor-%d@example.invalid", index), "Тестовый пользователь", rand.Text())
			results <- result{user: user, err: err}
		})
	}
	close(start)
	workers.Wait()
	close(results)
	admins := 0
	for current := range results {
		if current.err != nil {
			t.Fatal(current.err)
		}
		if current.user.Role == "admin" {
			admins++
		} else if current.user.Role != "user" {
			t.Fatalf("неожиданная роль %q", current.user.Role)
		}
	}
	var storedAdmins int
	if err := isolated.QueryRow(ctx, `SELECT count(*) FROM users u JOIN roles r ON r.id=u.role_id WHERE r.code='admin'`).Scan(&storedAdmins); err != nil {
		t.Fatal(err)
	}
	if admins != 1 || storedAdmins != 1 {
		t.Fatalf("администраторов в ответах %d, в БД %d, ожидался один", admins, storedAdmins)
	}
}
