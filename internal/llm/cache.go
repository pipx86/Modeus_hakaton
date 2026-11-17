package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
)

// CachedClient is an exact-match cache for successful LLM answers. The key is
// the serialized message list, so only identical prompts share a response.
type CachedClient struct {
	Next  ChatClient
	Max   int
	mu    sync.Mutex
	items map[string]string
	order []string
}

// NewCachedClient wraps next with a bounded in-memory cache.
func NewCachedClient(next ChatClient, max int) *CachedClient {
	if max <= 0 {
		max = 64
	}
	return &CachedClient{Next: next, Max: max, items: make(map[string]string)}
}

// Chat returns a cached answer for an identical prompt or delegates to Next.
func (c *CachedClient) Chat(ctx context.Context, messages []Message) (string, error) {
	if c == nil || c.Next == nil {
		return "", errors.New("llm: cached client has no next client")
	}
	key := cacheKey(messages)
	c.mu.Lock()
	if answer, ok := c.items[key]; ok {
		c.mu.Unlock()
		return answer, nil
	}
	c.mu.Unlock()

	answer, err := c.Next.Chat(ctx, messages)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	if _, exists := c.items[key]; !exists {
		c.items[key] = answer
		c.order = append(c.order, key)
		if len(c.order) > c.Max {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.items, oldest)
		}
	}
	c.mu.Unlock()
	return answer, nil
}

func cacheKey(messages []Message) string {
	data, _ := json.Marshal(messages)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
