package storage

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type Deduplicator interface {
	TryAcquire(messageID string) (bool, error)
}

type RedisDeduplicator struct {
	client *redis.Client
	ttl    time.Duration
}

func NewRedisDeduplicator(client *redis.Client) *RedisDeduplicator {
	return &RedisDeduplicator{
		client: client,
		ttl:    10 * time.Minute,
	}
}

func (d *RedisDeduplicator) TryAcquire(messageID string) (bool, error) {
	if messageID == "" {
		return true, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	key := "omniscan:msg:" + messageID
	acquired, err := d.client.SetNX(ctx, key, "1", d.ttl).Result()
	if err != nil {
		return false, err
	}
	return acquired, nil
}

type InMemoryDeduplicator struct {
	mu   sync.Mutex
	seen map[string]time.Time
	ttl  time.Duration
}

func NewInMemoryDeduplicator() *InMemoryDeduplicator {
	d := &InMemoryDeduplicator{
		seen: make(map[string]time.Time),
		ttl:  10 * time.Minute,
	}
	go d.cleanupLoop()
	return d
}

func (d *InMemoryDeduplicator) TryAcquire(messageID string) (bool, error) {
	if messageID == "" {
		return true, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if exp, ok := d.seen[messageID]; ok && time.Now().Before(exp) {
		return false, nil
	}

	d.seen[messageID] = time.Now().Add(d.ttl)
	return true, nil
}

func (d *InMemoryDeduplicator) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		d.mu.Lock()
		now := time.Now()
		for k, exp := range d.seen {
			if now.After(exp) {
				delete(d.seen, k)
			}
		}
		d.mu.Unlock()
	}
}
