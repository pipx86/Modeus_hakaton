package main

import (
	"context"
	"testing"
)

// TestConfigValidation проверяет, что сервер корректно обрабатывает невалидный конфиг.
func TestConfigValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config{
		address:     "", // Пустой адрес
		databaseURL: "", // Пустая БД
		jwtSecret:   "", // Пустой секрет
	}

	err := run(ctx, cfg)
	// Ожидаем ошибку, так как конфиг невалидный
	if err == nil {
		t.Fatal("ожидали ошибку при пустом конфиге, но сервер запустился")
	}
}

// TestRunWithInvalidDB проверяет обработку ошибки подключения к БД.
func TestRunWithInvalidDB(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config{
		address:     "127.0.0.1:0",
		databaseURL: "postgres://invalid:invalid@localhost:99999/nonexistent?sslmode=disable",
		jwtSecret:   "test-secret",
	}

	err := run(ctx, cfg)
	// Ожидаем ошибку подключения к БД
	if err == nil {
		t.Fatal("ожидали ошибку подключения к БД")
	}
}
