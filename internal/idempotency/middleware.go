package idempotency

import (
	"bytes"
	"io"
	"net/http"
	"time"
)

const headerName = "Idempotency-Key"

type recorder struct {
	http.ResponseWriter
	body   bytes.Buffer
	status int
}

func (r *recorder) Write(p []byte) (int, error) {
	n, err := r.body.Write(p)
	if err != nil {
		return n, err
	}
	return r.ResponseWriter.Write(p)
}

func (r *recorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func Middleware(store Store, ttl time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get(headerName)
		if key == "" || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodDelete {
			next.ServeHTTP(w, r)
			return
		}

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "unreadable body")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		hash := HashBody(bodyBytes)
		entry := &Entry{Key: key, BodyHash: hash, CreatedAt: time.Now(), TTL: ttl}

		existing, ok := store.Put(entry)
		switch {
		case !ok && existing != nil:
			writeErr(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "same key with different request body")
			return
		case ok && existing != nil && existing.Response != nil:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(existing.Status)
			_, _ = w.Write(existing.Response)
			return
		}

		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		if rec.status < 500 {
			_ = store.UpdateResponse(key, append([]byte(nil), rec.body.Bytes()...))
			if e := store.Get(key); e != nil {
				e.Status = rec.status
			}
		}
	})
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"code":"` + code + `","message":"` + msg + `"}`))
}
