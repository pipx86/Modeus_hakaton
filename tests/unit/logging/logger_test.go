package logging_test

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/logging"
)

func TestRotatingWriterCreatesSecondFileAfterSlice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	w := logging.NewRotatingWriter(path, 1)
	line := strings.Repeat("x", 1024) + "\n"

	for i := 0; i < 3000; i++ {
		if _, err := w.Write([]byte(line)); err != nil {
			_ = w.Close()
			t.Fatalf("write: %v", err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Архиватор сжимает предыдущие файлы уже после закрытия основного лога.
	deadline := time.Now().Add(3 * time.Second)
	for {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		archives, pending := 0, false
		for _, entry := range entries {
			if entry.Name() == filepath.Base(path) {
				continue
			}
			if strings.HasSuffix(entry.Name(), ".gz") {
				archives++
			} else {
				pending = true
			}
		}
		if archives > 0 && !pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("архивирование после ротации не завершилось: %v", entries)
		}
		time.Sleep(10 * time.Millisecond)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if info.Size() > 1<<20 {
		t.Fatalf("основной файл %d байт превышает лимит 1 МБ", info.Size())
	}
}

func TestInitWritesToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	writer := logging.NewRotatingWriter(path, 1)
	testLogger := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo}))
	testLogger.Info("hello rotation", "key", "value")

	if closer, ok := writer.(io.Closer); ok {
		closer.Close()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(data), "hello rotation") {
		t.Fatalf("в файле лога нет записи: %q", string(data))
	}
}

func TestLoggerNotNilByDefault(t *testing.T) {
	if logging.Logger == nil {
		t.Fatal("Logger обязан быть инициализирован пакетом при загрузке")
	}
	logging.Logger.Info("package init works")
}

// === НОВЫЕ ТЕСТЫ ДЛЯ ПОДНЯТИЯ ПОКРЫТИЯ (Windows-safe) ===

func TestLoggerDifferentLevels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "levels.log")

	writer := logging.NewRotatingWriter(path, 10) // Большой лимит, чтобы не ротировать
	defer func() {
		if c, ok := writer.(io.Closer); ok {
			c.Close()
		}
	}()

	testLogger := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelDebug}))

	testLogger.Debug("debug message")
	testLogger.Info("info message")
	testLogger.Warn("warn message")
	testLogger.Error("error message")

	// Даем время на запись
	time.Sleep(100 * time.Millisecond)

	data, _ := os.ReadFile(path)
	content := string(data)

	// Проверяем, что все уровни записались
	for _, msg := range []string{"debug message", "info message", "warn message", "error message"} {
		if !strings.Contains(content, msg) {
			t.Errorf("в логе отсутствует сообщение уровня: %s", msg)
		}
	}
}

func TestRotatingWriterEmptyWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.log")

	w := logging.NewRotatingWriter(path, 1)
	defer func() {
		if c, ok := w.(io.Closer); ok {
			c.Close()
		}
	}()

	// Пустая запись не должна вызывать панику
	n, err := w.Write([]byte{})
	if err != nil {
		t.Fatalf("пустая запись вернула ошибку: %v", err)
	}
	if n != 0 {
		t.Fatalf("пустая запись вернула n=%d, ожидалось 0", n)
	}
}

func TestRotatingWriterNilData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nil.log")

	w := logging.NewRotatingWriter(path, 1)
	defer func() {
		if c, ok := w.(io.Closer); ok {
			c.Close()
		}
	}()

	// Запись nil не должна вызывать панику
	n, err := w.Write(nil)
	if err != nil {
		t.Fatalf("запись nil вернула ошибку: %v", err)
	}
	if n != 0 {
		t.Fatalf("запись nil вернула n=%d, ожидалось 0", n)
	}
}
