package redis

import (
	"context"
	"errors"
	"fmt"
	"macocr/proxy/domain"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRateLimitFailureIsStorageUnavailable(t *testing.T) {
	repo, err := New("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	allowed, err := repo.Allow(ctx, "offline-test", 2)
	if allowed || !errors.Is(err, domain.ErrStorageUnavailable) {
		t.Fatalf("failure allowed=%v err=%v", allowed, err)
	}
}
func TestRedisRateLimitConcurrentFixedWindowAndPrincipalIsolation(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("set TEST_REDIS_URL for isolated live Redis rate-limit test")
	}
	repo, err := New(url)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	if err := repo.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("test-rate-%d", time.Now().UnixNano())
	var wg sync.WaitGroup
	var allowed atomic.Int32
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := repo.Allow(ctx, id, 7)
			if err != nil {
				t.Error(err)
			}
			if ok {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 7 {
		t.Fatalf("accepted %d requests with RPM7", allowed.Load())
	}
	if ok, err := repo.Allow(ctx, id+"-other", 1); err != nil || !ok {
		t.Fatal("independent principal rejected")
	}
	for i := 0; i < 3; i++ {
		if ok, err := repo.Allow(ctx, id, 0); err != nil || !ok {
			t.Fatal("zero-limit principal rejected")
		}
	}
	key := "ratelimit:" + id + ":" + time.Now().UTC().Format("200601021504")
	ttl, err := repo.client.TTL(ctx, key).Result()
	if err != nil || ttl <= 0 || ttl > 2*time.Minute {
		t.Fatalf("rate counter cleanup TTL=%v err=%v", ttl, err)
	}
	keys, err := repo.client.Keys(ctx, "ratelimit:"+id+"*").Result()
	if err == nil && len(keys) > 0 {
		repo.client.Del(ctx, keys...)
	}
}
