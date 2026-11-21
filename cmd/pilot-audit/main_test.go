package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"activity-descriptor/internal/pilot"
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

func TestРазборОфлайнФлагов(t *testing.T) {
	t.Run("фикстура не требует DSN", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "")
		opts, err := parseOptions([]string{"-fixture", "-profession", " Аналитик данных ", "-strict"}, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if !opts.fixture || !opts.offline() || !opts.strict || opts.profession != "Аналитик данных" {
			t.Fatalf("неверные параметры: %+v", opts)
		}
	})

	t.Run("снимок не требует DSN", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "")
		opts, err := parseOptions([]string{"-dataset", " testdata/pilot/sources-sample.json ", "-profession", "аналитик"}, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if opts.dataset != "testdata/pilot/sources-sample.json" || !opts.offline() {
			t.Fatalf("неверные параметры: %+v", opts)
		}
	})

	t.Run("в снимке ID программы может быть не UUID", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "")
		opts, err := parseOptions([]string{"-dataset", "dataset.json", "-program", "program-data-analyst"}, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if opts.program != "program-data-analyst" {
			t.Fatalf("неверные параметры: %+v", opts)
		}
	})

	t.Run("dataset и fixture взаимоисключающие", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "")
		_, err := parseOptions([]string{"-fixture", "-dataset", "dataset.json", "-profession", "аналитик"}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "одновременно") {
			t.Fatalf("ожидалась ошибка совмещения режимов, получено: %v", err)
		}
	})

	t.Run("база требует DSN даже с программой", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "")
		_, err := parseOptions([]string{"-program", testProgramID}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "DSN") {
			t.Fatalf("ожидалась ошибка DSN, получено: %v", err)
		}
	})
}

// writeTempDataset сохраняет снимок во временный файл.
func writeTempDataset(t *testing.T, document string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dataset.json")
	if err := os.WriteFile(path, []byte(document), 0o644); err != nil {
		t.Fatalf("записать снимок: %v", err)
	}
	return path
}

func TestFixtureОфлайнГотов(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	var stdout, stderr strings.Builder
	code := run([]string{"-profession", pilot.FixtureProfession, "-fixture"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run вернул %d, ожидался 0; stderr: %s", code, stderr.String())
	}
	for _, fragment := range []string{"Статус: готово", "Всего источников: 6", "найдено анализатором: 6", "Проблемы: нет"} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("в отчёте нет %q:\n%s", fragment, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr должен быть пуст: %q", stderr.String())
	}
}

func TestFixtureПрограммаОфлайнГотова(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	var stdout, stderr strings.Builder
	code := run([]string{"-fixture", "-program", pilot.FixtureProgramID}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run вернул %d, ожидался 0; stderr: %s", code, stderr.String())
	}
	for _, fragment := range []string{"Аудит программы", "Статус: готово", "Компетенции: 3, KSA: 5, связи с таксономией: 3"} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("в отчёте нет %q:\n%s", fragment, stdout.String())
		}
	}
}

func TestFixtureJSONОбеЦели(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	var stdout, stderr strings.Builder
	code := run([]string{"-fixture", "-profession", pilot.FixtureProfession, "-program", pilot.FixtureProgramID, "-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run вернул %d, ожидался 0; stderr: %s", code, stderr.String())
	}
	document := stdout.String()
	for _, fragment := range []string{`"sources"`, `"program"`, `"ready": true`, `"total": 6`} {
		if !strings.Contains(document, fragment) {
			t.Fatalf("в JSON нет %q:\n%s", fragment, document)
		}
	}
}

func TestСтрогийРежим(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	empty := writeTempDataset(t, `{"sources": []}`)

	var stdout, stderr strings.Builder
	if code := run([]string{"-dataset", empty, "-profession", "Аналитик данных"}, &stdout, &stderr); code != 0 {
		t.Fatalf("без -strict ожидался код 0, получен %d; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Статус: требует внимания") {
		t.Fatalf("пустой снимок должен быть «требует внимания»:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-dataset", empty, "-profession", "Аналитик данных", "-strict"}, &stdout, &stderr); code != 1 {
		t.Fatalf("со -strict ожидался код 1, получен %d", code)
	}
	if !strings.Contains(stdout.String(), "Статус: требует внимания") {
		t.Fatalf("отчёт должен быть напечатан до кода 1:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "-strict") {
		t.Fatalf("нет понятного сообщения о -strict: %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-fixture", "-profession", pilot.FixtureProfession, "-strict"}, &stdout, &stderr); code != 0 {
		t.Fatalf("готовая фикстура со -strict должна давать 0, получен %d; stderr: %s", code, stderr.String())
	}
}

func TestСнимкиИзРепозитория(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	root := filepath.Join("..", "..", "testdata", "pilot")

	var stdout, stderr strings.Builder
	if code := run([]string{"-dataset", filepath.Join(root, "sources-sample.json"), "-profession", "Аналитик данных"}, &stdout, &stderr); code != 0 {
		t.Fatalf("источники: run вернул %d; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Статус: готово") {
		t.Fatalf("пример источников должен быть готов:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-dataset", filepath.Join(root, "program-sample.json"), "-program", "program-data-analyst-2026"}, &stdout, &stderr); code != 0 {
		t.Fatalf("программа: run вернул %d; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Статус: готово") {
		t.Fatalf("пример программы должен быть готов:\n%s", stdout.String())
	}
}

func TestСнимокОшибки(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	tests := map[string]struct {
		document string
		want     string
	}{
		"битый JSON":   {document: `{"sources": [`, want: "разбор снимка"},
		"битая ссылка": {document: `{"analyzer_matches": ["ghost"]}`, want: "неизвестный источник"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			path := writeTempDataset(t, test.document)
			var stdout, stderr strings.Builder
			code := run([]string{"-dataset", path, "-profession", "Аналитик данных"}, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("run вернул %d, ожидался 2", code)
			}
			if stdout.Len() != 0 {
				t.Fatalf("при ошибке снимка отчёт не печатается: %q", stdout.String())
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr = %q, ожидалось %q", stderr.String(), test.want)
			}
		})
	}
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
