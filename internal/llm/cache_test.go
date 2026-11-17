package llm

import (
	"context"
	"errors"
	"testing"
)

type cacheFakeClient struct {
	calls  int
	answer string
	err    error
}

func (c *cacheFakeClient) Chat(context.Context, []Message) (string, error) {
	c.calls++
	return c.answer, c.err
}

func TestCachedClientHitsExactPrompt(t *testing.T) {
	next := &cacheFakeClient{answer: `{"ok":true}`}
	client := NewCachedClient(next, 2)
	messages := []Message{{Role: RoleUser, Content: "hello"}}
	first, err := client.Chat(context.Background(), messages)
	if err != nil || first != `{"ok":true}` {
		t.Fatalf("first = %q, %v", first, err)
	}
	second, err := client.Chat(context.Background(), messages)
	if err != nil || second != first {
		t.Fatalf("second = %q, %v", second, err)
	}
	if next.calls != 1 {
		t.Fatalf("calls = %d, want 1", next.calls)
	}
}

func TestCachedClientDoesNotCacheErrors(t *testing.T) {
	next := &cacheFakeClient{err: errors.New("provider down")}
	client := NewCachedClient(next, 2)
	_, _ = client.Chat(context.Background(), []Message{{Role: RoleUser, Content: "hello"}})
	_, _ = client.Chat(context.Background(), []Message{{Role: RoleUser, Content: "hello"}})
	if next.calls != 2 {
		t.Fatalf("calls = %d, want 2", next.calls)
	}
}

func TestCachedClientRequiresNext(t *testing.T) {
	if _, err := (&CachedClient{}).Chat(context.Background(), nil); err == nil {
		t.Fatal("expected configuration error")
	}
}
