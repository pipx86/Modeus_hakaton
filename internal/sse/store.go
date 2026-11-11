package sse

import (
	"sync"
)

type EventStore interface {
	Append(event *SSEEvent) error
	GetAfter(lastEventID string, limit int) ([]*SSEEvent, error)
	SubscribeLive(fn func(*SSEEvent)) (unsubscribe func())
	Close() error
}

type MemEventStore struct {
	mu       sync.RWMutex
	buffer   []*SSEEvent
	capacity int
	head     int
	size     int

	listenersMu sync.Mutex
	listeners   map[int]func(*SSEEvent)
	listenerSeq int
}

func NewMemEventStore(capacity int) *MemEventStore {
	if capacity <= 0 {
		capacity = 1000
	}
	return &MemEventStore{
		buffer:    make([]*SSEEvent, capacity),
		capacity:  capacity,
		listeners: make(map[int]func(*SSEEvent)),
	}
}

func (s *MemEventStore) Append(event *SSEEvent) error {
	s.mu.Lock()
	s.buffer[s.head] = event
	s.head = (s.head + 1) % s.capacity
	if s.size < s.capacity {
		s.size++
	}
	s.mu.Unlock()

	s.broadcast(event)
	return nil
}

func (s *MemEventStore) GetAfter(lastEventID string, limit int) ([]*SSEEvent, error) {
	if limit <= 0 {
		limit = 100
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.size == 0 {
		return nil, nil
	}

	startPos := -1
	for i := 0; i < s.size; i++ {
		idx := (s.head - s.size + i + s.capacity) % s.capacity
		ev := s.buffer[idx]
		if ev != nil && ev.ID == lastEventID {
			startPos = idx
			break
		}
	}

	readFrom := startPos
	if readFrom == -1 {
		readFrom = (s.head - s.size + s.capacity) % s.capacity
	} else {
		readFrom = (readFrom + 1) % s.capacity
	}

	out := make([]*SSEEvent, 0, limit)
	count := 0
	current := readFrom
	for count < s.size && len(out) < limit {
		ev := s.buffer[current]
		if ev != nil {
			out = append(out, ev)
		}
		current = (current + 1) % s.capacity
		count++
	}

	return out, nil
}

func (s *MemEventStore) SubscribeLive(fn func(*SSEEvent)) func() {
	s.listenersMu.Lock()
	defer s.listenersMu.Unlock()
	s.listenerSeq++
	id := s.listenerSeq
	s.listeners[id] = fn
	return func() {
		s.listenersMu.Lock()
		delete(s.listeners, id)
		s.listenersMu.Unlock()
	}
}

func (s *MemEventStore) Close() error { return nil }

func (s *MemEventStore) broadcast(ev *SSEEvent) {
	s.listenersMu.Lock()
	callbacks := make([]func(*SSEEvent), 0, len(s.listeners))
	for _, fn := range s.listeners {
		callbacks = append(callbacks, fn)
	}
	s.listenersMu.Unlock()

	for _, fn := range callbacks {
		go fn(ev)
	}
}
