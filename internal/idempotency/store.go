package idempotency

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"time"
)

var ErrConflict = errors.New("IDEMPOTENCY_CONFLICT")

type Entry struct {
	Key       string
	BodyHash  string
	Response  []byte
	Status    int
	Headers   http.Header
	CreatedAt time.Time
	TTL       time.Duration
}

func (e *Entry) Expired(now time.Time) bool {
	return now.After(e.CreatedAt.Add(e.TTL))
}

type Store interface {
	Put(entry *Entry) (existing *Entry, ok bool)
	Get(key string) *Entry
	UpdateResponse(key string, resp []byte, status int, headers http.Header) error
}

func HashBody(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type MemStore struct {
	mu      sync.RWMutex
	entries map[string]*Entry
	now     func() time.Time
}

func NewMemStore(ttl time.Duration) *MemStore {
	return &MemStore{
		entries: map[string]*Entry{},
		now:     func() time.Time { return time.Now() },
	}
}

func (m *MemStore) SetNowForTests(f func() time.Time) { m.now = f }

func (m *MemStore) Put(e *Entry) (*Entry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	if old, ok := m.entries[e.Key]; ok {
		if old.Expired(now) {
			delete(m.entries, e.Key)
		} else if old.BodyHash != e.BodyHash {
			return old, false
		} else {
			return old, true
		}
	}
	m.entries[e.Key] = e
	return nil, true
}

func (m *MemStore) Get(key string) *Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[key]
	if !ok || e.Expired(m.now()) {
		return nil
	}
	return e
}

func (m *MemStore) UpdateResponse(key string, resp []byte, status int, headers http.Header) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok {
		return errors.New("entry not found")
	}
	e.Response = make([]byte, len(resp))
	copy(e.Response, resp)
	e.Status = status
	e.Headers = headers.Clone()
	return nil
}
