package idempotency

import (
	"bytes"
	"io"
	"net/http"
	"sync"
	"time"
)

var statusCache sync.Map

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
	r.status = code
}

func (r *recorder) Write(b []byte) (int, error) {
	return r.body.Write(b)
}

func Middleware(store Store, ttl time.Duration, next http.Handler) http.Handler {
	var mu sync.Mutex
	inFlight := make(map[string]chan struct{})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}

		bodyBytes, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		hash := HashBody(bodyBytes)

		mu.Lock()
		ch, inflight := inFlight[key]
		if !inflight {
			ch = make(chan struct{})
			inFlight[key] = ch
			mu.Unlock()

			entry := &Entry{
				Key:       key,
				BodyHash:  hash,
				CreatedAt: time.Now(),
				TTL:       ttl,
			}

			existing, ok := store.Put(entry)

			if !ok && existing != nil {
				writeErr(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "same key with different body")
				mu.Lock()
				delete(inFlight, key)
				close(ch)
				mu.Unlock()
				return
			}

			if ok && existing != nil && existing.Response != nil {
				statusCode := getStatusForReplay(key, existing.Status)
				w.WriteHeader(statusCode)
				_, _ = w.Write(existing.Response)
				mu.Lock()
				delete(inFlight, key)
				close(ch)
				mu.Unlock()
				return
			}

			rec := &recorder{}
			next.ServeHTTP(rec, r)

			respData := rec.body.Bytes()
			statusCode := rec.status
			if statusCode == 0 {
				statusCode = http.StatusOK
			}

			statusCache.Store(key, statusCode)
			_ = store.UpdateResponse(key, respData)

			for k, v := range rec.Header() {
				w.Header()[k] = v
			}
			w.WriteHeader(statusCode)
			_, _ = w.Write(respData)

			mu.Lock()
			delete(inFlight, key)
			close(ch)
			mu.Unlock()
			return
		}

		mu.Unlock()

		<-ch

		finalEntry := store.Get(key)
		if finalEntry == nil || finalEntry.Response == nil {
			writeErr(w, http.StatusInternalServerError, "IDEMPOTENCY_ERROR", "first request failed without storing result")
			return
		}

		statusCode := getStatusForReplay(key, finalEntry.Status)

		w.WriteHeader(statusCode)
		_, _ = w.Write(finalEntry.Response)
	})
}

func getStatusForReplay(key string, storedStatus int) int {
	if storedStatus > 0 {
		return storedStatus
	}
	if val, ok := statusCache.Load(key); ok {
		return val.(int)
	}
	return http.StatusOK
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"code":"` + code + `","message":"` + msg + `"}`))
}
