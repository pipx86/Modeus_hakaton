package idempotency

import (
	"testing"
	"time"
)

func TestPutAndGetSameKeySameBody(t *testing.T) {
	s := NewMemStore(time.Minute)
	e1 := &Entry{Key: "k1", BodyHash: "h1", CreatedAt: time.Now(), TTL: time.Minute}
	existing, ok := s.Put(e1)
	if !ok || existing != nil {
		t.Fatalf("первый put должен был пройти чисто")
	}
	got := s.Get("k1")
	if got == nil || got.BodyHash != "h1" {
		t.Fatalf("get не нашёл запись")
	}

	e2 := &Entry{Key: "k1", BodyHash: "h1", CreatedAt: time.Now(), TTL: time.Minute}
	existing, ok = s.Put(e2)
	if !ok {
		t.Fatalf("повтор идентичного запроса должен быть принят как hit")
	}
	if existing == nil || existing.BodyHash != "h1" {
		t.Fatalf("должна была вернуться первая запись")
	}
}

func TestPutConflictDifferentBody(t *testing.T) {
	s := NewMemStore(time.Minute)
	_, _ = s.Put(&Entry{Key: "k1", BodyHash: "AAA", CreatedAt: time.Now(), TTL: time.Minute})
	existing, ok := s.Put(&Entry{Key: "k1", BodyHash: "BBB", CreatedAt: time.Now(), TTL: time.Minute})
	if ok {
		t.Fatalf("конфликт body обязан привести к ok=false")
	}
	if existing == nil || existing.BodyHash != "AAA" {
		t.Fatalf("должна вернуться исходная запись для диагностики конфликта")
	}
}

func TestTTLEviction(t *testing.T) {
	base := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	s := NewMemStore(30 * time.Second)
	s.SetNowForTests(func() time.Time { return base })

	_, _ = s.Put(&Entry{Key: "k1", BodyHash: "h1", CreatedAt: base, TTL: 30 * time.Second})
	if s.Get("k1") == nil {
		t.Fatalf("запись должна быть видна сразу")
	}

	newNow := base.Add(31 * time.Second)
	s.SetNowForTests(func() time.Time { return newNow })
	if s.Get("k1") != nil {
		t.Fatalf("запись должна истечь после TTL")
	}

	existing, ok := s.Put(&Entry{Key: "k1", BodyHash: "NEWBODY", CreatedAt: newNow, TTL: 30 * time.Second})
	if !ok || existing != nil {
		t.Fatalf("после истечения TTL новый put должен занять слот")
	}
}

func TestConcurrentPutsRace(t *testing.T) {
	s := NewMemStore(time.Minute)
	const N = 50
	results := make(chan struct {
		ok       bool
		existing bool
	}, N)

	for i := 0; i < N; i++ {
		go func() {
			e := &Entry{Key: "race-key", BodyHash: "same-body", CreatedAt: time.Now(), TTL: time.Minute}
			existing, ok := s.Put(e)
			results <- struct {
				ok       bool
				existing bool
			}{ok, existing != nil}
		}()
	}

	var firstCreates, hits int
	for i := 0; i < N; i++ {
		res := <-results
		if !res.ok {
			t.Fatalf("гонка не должна порождать конфликтов при одинаковом body")
		}
		if res.existing {
			hits++
		} else {
			firstCreates++
		}
	}
	if firstCreates != 1 {
		t.Fatalf("ровно один goroutine должен создать запись, получили %d созданий", firstCreates)
	}
	if hits != N-1 {
		t.Fatalf("остальные должны были попасть в существующую запись, получили %d", hits)
	}
}
