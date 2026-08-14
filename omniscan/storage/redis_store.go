package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisQuotaStore struct {
	Client *redis.Client
}

func NewRedisQuotaStore(redisURL string) (*RedisQuotaStore, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis URL: %w", err)
	}

	rdb := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &RedisQuotaStore{Client: rdb}, nil
}

func (r *RedisQuotaStore) GetOrCreateUserConfig(userID string, defaultScanLimit, defaultAskLimit int) (int, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	key := "omniscan:userconfig:" + userID
	vals, err := r.Client.HMGet(ctx, key, "daily_scan_limit", "session_ask_limit").Result()
	if err == nil && len(vals) == 2 && vals[0] != nil && vals[1] != nil {
		scanLimit, _ := strconv.Atoi(fmt.Sprintf("%v", vals[0]))
		askLimit, _ := strconv.Atoi(fmt.Sprintf("%v", vals[1]))
		if scanLimit > 0 && askLimit > 0 {
			return scanLimit, askLimit, nil
		}
	}

	_ = r.Client.HSet(ctx, key, map[string]any{
		"daily_scan_limit":  defaultScanLimit,
		"session_ask_limit": defaultAskLimit,
	}).Err()

	return defaultScanLimit, defaultAskLimit, nil
}

func (r *RedisQuotaStore) CheckAndIncrementQuota(userID string, dailyLimit int) (bool, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	today := time.Now().Format("2006-01-02")
	key := fmt.Sprintf("omniscan:quota:%s:%s", today, userID)

	script := redis.NewScript(`
		local current = redis.call('GET', KEYS[1])
		if current and tonumber(current) >= tonumber(ARGV[1]) then
			return tonumber(current)
		end
		local newval = redis.call('INCR', KEYS[1])
		if tonumber(newval) == 1 then
			redis.call('EXPIRE', KEYS[1], 86400)
		end
		return newval
	`)

	res, err := script.Run(ctx, r.Client, []string{key}, dailyLimit).Int64()
	if err != nil {
		return false, 0, fmt.Errorf("redis quota script: %w", err)
	}

	currentCount := int(res)
	if currentCount > dailyLimit {
		return false, dailyLimit, nil
	}

	return true, currentCount, nil
}

func (r *RedisQuotaStore) GetQuota(userID string, dailyLimit int) (int, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	today := time.Now().Format("2006-01-02")
	key := fmt.Sprintf("omniscan:quota:%s:%s", today, userID)

	val, err := r.Client.Get(ctx, key).Result()
	if err == redis.Nil {
		return 0, dailyLimit, nil
	} else if err != nil {
		return 0, 0, err
	}

	count, _ := strconv.Atoi(val)
	remaining := dailyLimit - count
	if remaining < 0 {
		remaining = 0
	}
	return count, remaining, nil
}

func (r *RedisQuotaStore) RefundQuota(userID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	today := time.Now().Format("2006-01-02")
	key := fmt.Sprintf("omniscan:quota:%s:%s", today, userID)

	script := redis.NewScript(`
		local current = redis.call('GET', KEYS[1])
		if current and tonumber(current) > 0 then
			return redis.call('DECR', KEYS[1])
		end
		return 0
	`)

	return script.Run(ctx, r.Client, []string{key}).Err()
}

func (r *RedisQuotaStore) Close() error {
	return r.Client.Close()
}

type RedisSharedStore struct {
	client *redis.Client
}

func NewRedisSharedStore(client *redis.Client) *RedisSharedStore {
	return &RedisSharedStore{client: client}
}

func (s *RedisSharedStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	val, err := s.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	return val, true, nil
}

func (s *RedisSharedStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return s.client.Set(ctx, key, value, ttl).Err()
}

func (s *RedisSharedStore) Delete(ctx context.Context, key string) error {
	return s.client.Del(ctx, key).Err()
}

// RedisSessionStore for multi-replica distributed scan sessions
type RedisSessionStore struct {
	client *redis.Client
	ttl    time.Duration
}

func NewRedisSessionStore(client *redis.Client) *RedisSessionStore {
	return &RedisSessionStore{
		client: client,
		ttl:    24 * time.Hour,
	}
}

func (s *RedisSessionStore) CreateSession(sessionID, userID, documentID, docType, ocrText string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sess := ScanSession{
		SessionID:  sessionID,
		UserID:     userID,
		DocumentID: documentID,
		DocType:    docType,
		OCRText:    ocrText,
		AskCount:   0,
		CreatedAt:  time.Now(),
	}

	b, err := json.Marshal(sess)
	if err != nil {
		return err
	}

	key := "omniscan:session:" + sessionID
	return s.client.Set(ctx, key, b, s.ttl).Err()
}

func (s *RedisSessionStore) GetSession(sessionID string) (*ScanSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	key := "omniscan:session:" + sessionID
	b, err := s.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	var sess ScanSession
	if err := json.Unmarshal(b, &sess); err != nil {
		return nil, err
	}

	return &sess, nil
}

func (s *RedisSessionStore) CheckAndIncrementAskQuota(sessionID string, maxAsks int) (bool, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	key := "omniscan:session:" + sessionID
	b, err := s.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return false, 0, nil
	} else if err != nil {
		return false, 0, err
	}

	var sess ScanSession
	if err := json.Unmarshal(b, &sess); err != nil {
		return false, 0, err
	}

	if sess.AskCount >= maxAsks {
		_ = s.client.Del(ctx, key).Err() // Auto purge on max ask limit reached
		return false, sess.AskCount, nil
	}

	sess.AskCount++
	newB, err := json.Marshal(sess)
	if err != nil {
		return false, 0, err
	}

	if err := s.client.Set(ctx, key, newB, s.ttl).Err(); err != nil {
		return false, 0, err
	}

	return true, sess.AskCount, nil
}

func (s *RedisSessionStore) DeleteSession(sessionID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	key := "omniscan:session:" + sessionID
	return s.client.Del(ctx, key).Err()
}

func (s *RedisSessionStore) Close() error {
	return nil
}
