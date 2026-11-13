package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingWriterCreatesSecondFileAfterSlice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	w := NewRotatingWriter(path, 1)
	line := strings.Repeat("x", 1024) + "\n"
	for i := 0; i < 3000; i++ {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("ожидали минимум 2 файла после среза (основной + резервный), получили %d", len(entries))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() > 1<<20 {
		t.Fatalf("основной файл %d байт превышает лимит среза 1 МБ", info.Size())
	}
}

func TestInitWritesToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	// Создаём writer напрямую, чтобы иметь возможность закрыть его
	writer := NewRotatingWriter(path, 1)

	// Пишем через slog напрямую в этот writer
	testLogger := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo}))
	testLogger.Info("hello rotation", "key", "value")

	// Закрываем writer, чтобы Windows отпустила файл
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
	if Logger == nil {
		t.Fatal("Logger обязан быть инициализирован пакетом при загрузке")
	}
	Logger.Info("package init works")
}
