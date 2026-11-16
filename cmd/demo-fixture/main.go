// Команда demo-fixture создаёт вымышленные демонстрационные данные.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"activity-descriptor/internal/demofill"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "переменная DATABASE_URL не задана")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "неверные настройки PostgreSQL")
		os.Exit(2)
	}
	defer pool.Close()
	created, err := demofill.Apply(ctx, pool)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if created {
		fmt.Println("Созданы синтетическое исследование, дерево и подтверждения.")
	} else {
		fmt.Println("Демонстрационные данные уже существуют и совпадают; новые записи не добавлены.")
	}
}
