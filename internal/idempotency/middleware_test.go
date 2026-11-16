package idempotency

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type countingHandler struct {
	mu    sync.Mutex
	calls int
}

func (c *countingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{"created":true,"call":` + itoa(c.calls) + `}`))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func TestMiddlewareReplayDoesNotCallInner(t *testing.T) {
	store := NewMemStore(time.Minute)
	inner := &countingHandler{}
	h := Middleware(store, time.Minute, inner)

	body := `{"profession":"cook"}`
	doReq := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/runs", strings.NewReader(body))
		req.Header.Set("Idempotency-Key", "abc")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	rr1 := doReq()
	if rr1.Code != http.StatusCreated {
		t.Fatalf("первый запрос ожидали 201, получили %d", rr1.Code)
	}
	rr2 := doReq()
	if rr2.Code != http.StatusCreated {
		t.Fatalf("повтор должен вернуть тот же статус, получили %d", rr2.Code)
	}
	if rr1.Body.String() != rr2.Body.String() {
		t.Fatalf("тело повтора обязано совпадать: %q vs %q", rr1.Body.String(), rr2.Body.String())
	}
	if inner.calls != 1 {
		t.Fatalf("business-handler должен быть дёрнут ровно 1 раз, получили %d", inner.calls)
	}
}

func TestMiddlewareConflictDifferentBody(t *testing.T) {
	store := NewMemStore(time.Minute)
	inner := &countingHandler{}
	h := Middleware(store, time.Minute, inner)

	req1 := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"a":1}`))
	req1.Header.Set("Idempotency-Key", "kk")
	rr1 := httptest.NewRecorder()
	h.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusCreated {
		t.Fatalf("первый запрос упал: %d", rr1.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"a":2}`))
	req2.Header.Set("Idempotency-Key", "kk")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusConflict {
		t.Fatalf("другой body под тем же ключом обязан дать 409, получили %d (%s)", rr2.Code, rr2.Body.String())
	}
	if inner.calls != 1 {
		t.Fatalf("handler не должен дёргаться на конфликте, получили %d вызовов", inner.calls)
	}
}

func TestMiddlewareNoKeyPassesThrough(t *testing.T) {
	store := NewMemStore(time.Minute)
	inner := &countingHandler{}
	h := Middleware(store, time.Minute, inner)

	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated || inner.calls != 1 {
		t.Fatalf("без Idempotency-Key middleware прозрачен: code=%d calls=%d", rr.Code, inner.calls)
	}
}

func TestMiddlewareConcurrentIdenticalRequests(t *testing.T) {
	store := NewMemStore(time.Minute)
	inner := &countingHandler{}
	h := Middleware(store, time.Minute, inner)

	const N = 10
	var wg sync.WaitGroup
	codes := make(chan int, N)
	bodies := make(chan string, N)

	start := make(chan struct{})
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{" profession ":" cook "}`))
			req.Header.Set("Idempotency-Key", "race")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			codes <- rr.Code
			b, _ := io.ReadAll(rr.Body)
			bodies <- string(b)
		}()
	}
	close(start)
	wg.Wait()
	close(codes)
	close(bodies)

	if inner.calls != 1 {
		t.Fatalf("business-handler обязан сработать 1 раз при гонке, получили %d", inner.calls)
	}
	firstBody := ""
	for b := range bodies {
		if firstBody == "" {
			firstBody = b
		} else if b != firstBody {
			t.Fatalf("ответы в гонке разошлись: %q vs %q", firstBody, b)
		}
	}
	for c := range codes {
		if c != http.StatusCreated {
			t.Fatalf("все ответы обязаны быть 201, поймали %d", c)
		}
	}
}
