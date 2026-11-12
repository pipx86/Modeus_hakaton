package logging

import (
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

var (
	Logger *slog.Logger
	once   sync.Once
)

// Init явная инициализация (вызывается из main.go)
func Init() {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	Logger = slog.New(slog.NewJSONHandler(os.Stdout, opts))
	slog.SetDefault(Logger)
}

// L безопасный доступ к логгеру (если не инициализирован — инициализирует тихо)
func L() *slog.Logger {
	once.Do(func() {
		if Logger == nil {
			Init()
		}
	})
	return Logger
}

// Middleware логирует каждый запрос с контекстом
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseRecorder{ResponseWriter: w, status: 200}

		next.ServeHTTP(rec, r)

		duration := time.Since(start)
		L().Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", duration.Milliseconds(),
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
