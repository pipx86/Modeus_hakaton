package idempotency

import (
	"activity-descriptor/internal/auth"
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type recorder struct {
	headerMap http.Header
	body      bytes.Buffer
	status    int
}

func (r *recorder) Header() http.Header {
	if r.headerMap == nil {
		r.headerMap = make(http.Header)
	}
	return r.headerMap
}
func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}
func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(b)
}

// Middleware выполняет повторный запрос только один раз в пределах срока хранения.
func Middleware(store Store, ttl time.Duration, next http.Handler) http.Handler {
	var mu sync.Mutex
	inFlight := make(map[string]chan struct{})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" || r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		if len(key) > 256 || strings.TrimSpace(key) == "" {
			writeErr(w, 400, "BAD_REQUEST", "недопустимый ключ")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20+1))
		if err != nil || len(body) > 4<<20 {
			writeErr(w, 400, "BAD_REQUEST", "не удалось прочитать запрос или он слишком большой")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		q := r.URL.Query()
		q.Del("access_token")
		role, _ := auth.FromContext(r.Context())
		scope := auth.Subject(r.Context()) + "\x00" + string(role) + "\x00" + r.Method + "\x00" + r.URL.EscapedPath() + "\x00" + q.Encode() + "\x00" + key
		key = HashBody([]byte(scope))
		hash := HashBody(body)
		replay := func(e *Entry) {
			for k, v := range e.Headers {
				w.Header()[k] = append([]string(nil), v...)
			}
			w.WriteHeader(e.Status)
			_, _ = w.Write(e.Response)
		}
		mu.Lock()
		ch, inflight := inFlight[key]
		if inflight {
			mu.Unlock()
			select {
			case <-ch:
			case <-r.Context().Done():
				return
			}
			entry := store.Get(key)
			if entry == nil || entry.Response == nil {
				writeErr(w, 500, "IDEMPOTENCY_ERROR", "результат первого запроса не сохранён")
				return
			}
			if entry.BodyHash != hash {
				writeErr(w, 409, "IDEMPOTENCY_CONFLICT", "ключ уже использован с другим содержимым")
				return
			}
			replay(entry)
			return
		}
		ch = make(chan struct{})
		inFlight[key] = ch
		mu.Unlock()
		defer func() { mu.Lock(); delete(inFlight, key); close(ch); mu.Unlock() }()
		entry := &Entry{Key: key, BodyHash: hash, CreatedAt: time.Now(), TTL: ttl}
		existing, ok := store.Put(entry)
		if !ok {
			writeErr(w, 409, "IDEMPOTENCY_CONFLICT", "ключ уже использован с другим содержимым")
			return
		}
		if existing != nil && existing.Response != nil {
			replay(existing)
			return
		}
		rec := &recorder{}
		next.ServeHTTP(rec, r)
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		if err := store.UpdateResponse(key, rec.body.Bytes(), status, rec.Header()); err != nil {
			writeErr(w, 500, "IDEMPOTENCY_ERROR", "результат запроса не сохранён")
			return
		}
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(status)
		_, _ = w.Write(rec.body.Bytes())
	})
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Поля задаются самим сервером и не содержат пользовательского ввода.
	_, _ = w.Write([]byte(`{"code":"` + code + `","message":"` + msg + `"}`))
}
