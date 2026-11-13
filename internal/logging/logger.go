package logging

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

var Logger *slog.Logger

func init() {
	Logger = newLogger(os.Stdout, levelFromEnv())
	slog.SetDefault(Logger)
}

func Init() {
	file := os.Getenv("LOG_FILE")
	if file == "" {
		file = filepath.Join("logs", "app.log")
	}
	maxMB := 1024
	if v := os.Getenv("LOG_MAX_SIZE_MB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxMB = n
		}
	}

	writer := NewRotatingWriter(file, maxMB)
	Logger = newLogger(io.MultiWriter(os.Stdout, writer), levelFromEnv())
	slog.SetDefault(Logger)
	Logger.Info("logging initialized", "file", file, "max_size_mb", maxMB)
}

func NewRotatingWriter(path string, maxSizeMB int) io.WriteCloser {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	return &lumberjack.Logger{
		Filename:   path,
		MaxSize:    maxSizeMB,
		MaxBackups: 5,
		MaxAge:     30,
		Compress:   true,
		LocalTime:  true,
	}
}

func L() *slog.Logger {
	if Logger == nil {
		Logger = newLogger(os.Stdout, levelFromEnv())
	}
	return Logger
}

func newLogger(w io.Writer, lvl slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lvl}))
}

func levelFromEnv() slog.Level {
	lvl := slog.LevelInfo
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		if err := lvl.UnmarshalText([]byte(v)); err != nil {
			return slog.LevelInfo
		}
	}
	return lvl
}

func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		L().Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote_addr", r.RemoteAddr,
		)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush передаёт данные потока клиенту, не дожидаясь завершения запроса.
func (r *responseRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap позволяет стандартному контроллеру ответа работать с исходным writer.
func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
