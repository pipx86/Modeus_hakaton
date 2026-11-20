package main

import (
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
)

const testProgramID = "123e4567-e89b-12d3-a456-426614174000"

func TestРазборФлагов(t *testing.T) {
	t.Run("профессия и DSN из окружения", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://env/db")
		opts, err := parseOptions([]string{"-profession", "  аналитик данных  ", "-json"}, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if opts.profession != "аналитик данных" || !opts.json || opts.program != "" {
			t.Fatalf("неверные параметры: %+v", opts)
		}
		if opts.dsn != "postgres://env/db" {
			t.Fatalf("DSN должен браться из окружения: %q", opts.dsn)
		}
	})

	t.Run("явный DSN важнее окружения", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://env/db")
		opts, err := parseOptions([]string{"-profession", "аналитик", "-dsn", " postgres://flag/db "}, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if opts.dsn != "postgres://flag/db" {
			t.Fatalf("флаг -dsn не применён: %q", opts.dsn)
		}
	})

	t.Run("программа и корректный UUID", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://env/db")
		opts, err := parseOptions([]string{"-program", " " + testProgramID + " "}, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if opts.program != testProgramID || opts.profession != "" {
			t.Fatalf("неверные параметры: %+v", opts)
		}
	})

	t.Run("нужна хотя бы одна цель", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://env/db")
		_, err := parseOptions([]string{"-json"}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "хотя бы") {
			t.Fatalf("ожидалась ошибка цели, получено: %v", err)
		}
	})

	t.Run("пустой DSN", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "")
		_, err := parseOptions([]string{"-profession", "аналитик"}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "DSN") {
			t.Fatalf("ожидалась ошибка DSN, получено: %v", err)
		}
	})

	t.Run("некорректный UUID программы", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://env/db")
		_, err := parseOptions([]string{"-program", "not-a-uuid"}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "UUID") {
			t.Fatalf("ожидалась ошибка UUID, получено: %v", err)
		}
	})

	t.Run("неизвестный аргумент", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://env/db")
		_, err := parseOptions([]string{"-profession", "аналитик", "лишний"}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "неизвестные аргументы") {
			t.Fatalf("ожидалась ошибка аргументов, получено: %v", err)
		}
	})

	t.Run("справка", func(t *testing.T) {
		_, err := parseOptions([]string{"-h"}, io.Discard)
		if !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("ожидался flag.ErrHelp, получено: %v", err)
		}
	})
}

func TestКодВозвратаПриОшибкеКонфигурации(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	var stdout, stderr strings.Builder
	code := run(nil, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("без цели ожидался код 2, получен %d", code)
	}
	if !strings.Contains(stderr.String(), "укажите -profession и/или -program") {
		t.Fatalf("нет понятного сообщения: %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("при ошибке конфигурации отчёт не печатается: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"-profession", "аналитик"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("без DSN ожидался код 2, получен %d", code)
	}
	if !strings.Contains(stderr.String(), "DSN") {
		t.Fatalf("нет понятного сообщения о DSN: %q", stderr.String())
	}
}
