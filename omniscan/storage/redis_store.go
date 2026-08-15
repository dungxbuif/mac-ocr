package storage

import (
	"context"
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

func (r *RedisQuotaStore) GetOrCreateUserConfig(userID string, defScan, defOCR, defAsk int) (int, int, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	key := "omniscan:userconfig:" + userID
	vals, err := r.Client.HMGet(ctx, key, "daily_scan_limit", "daily_ocr_limit", "session_ask_limit").Result()
	if err == nil && len(vals) == 3 && vals[0] != nil && vals[1] != nil && vals[2] != nil {
		scanLimit, _ := strconv.Atoi(fmt.Sprintf("%v", vals[0]))
		ocrLimit, _ := strconv.Atoi(fmt.Sprintf("%v", vals[1]))
		askLimit, _ := strconv.Atoi(fmt.Sprintf("%v", vals[2]))
		if scanLimit > 0 && ocrLimit > 0 && askLimit > 0 {
			return scanLimit, ocrLimit, askLimit, nil
		}
	}

	_ = r.Client.HSet(ctx, key, map[string]any{
		"daily_scan_limit":  defScan,
		"daily_ocr_limit":   defOCR,
		"session_ask_limit": defAsk,
	}).Err()

	return defScan, defOCR, defAsk, nil
}

// checkAndIncrement is the shared Lua implementation for *scan and *ocr
// counters; `field` selects the per-day hash field to bump. The script is
// atomic across replicas: GET/INCR/EXPIRE run as one server-side block.
func (r *RedisQuotaStore) checkAndIncrement(userID, field string, dailyLimit int) (bool, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	today := time.Now().Format("2006-01-02")
	key := fmt.Sprintf("omniscan:quota:%s:%s", today, userID)

	script := redis.NewScript(`
		local current = redis.call('HGET', KEYS[1], ARGV[1])
		if current and tonumber(current) >= tonumber(ARGV[2]) then
			return tonumber(current)
		end
		local newval = redis.call('HINCRBY', KEYS[1], ARGV[1], 1)
		if tonumber(newval) == 1 then
			redis.call('EXPIRE', KEYS[1], 86400)
		end
		return newval
	`)

	res, err := script.Run(ctx, r.Client, []string{key}, field, dailyLimit).Int64()
	if err != nil {
		return false, 0, fmt.Errorf("redis quota script: %w", err)
	}
	currentCount := int(res)
	if currentCount > dailyLimit {
		return false, dailyLimit, nil
	}
	return true, currentCount, nil
}

func (r *RedisQuotaStore) CheckAndIncrementScanQuota(userID string, dailyLimit int) (bool, int, error) {
	return r.checkAndIncrement(userID, "scan_count", dailyLimit)
}

func (r *RedisQuotaStore) CheckAndIncrementOCRQuota(userID string, dailyLimit int) (bool, int, error) {
	return r.checkAndIncrement(userID, "ocr_count", dailyLimit)
}

func (r *RedisQuotaStore) GetQuota(userID string, scanLimit, ocrLimit int) (int, int, int, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	today := time.Now().Format("2006-01-02")
	key := fmt.Sprintf("omniscan:quota:%s:%s", today, userID)

	vals, err := r.Client.HMGet(ctx, key, "scan_count", "ocr_count").Result()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	scanCount, ocrCount := 0, 0
	if len(vals) == 2 {
		if vals[0] != nil {
			scanCount, _ = strconv.Atoi(fmt.Sprintf("%v", vals[0]))
		}
		if vals[1] != nil {
			ocrCount, _ = strconv.Atoi(fmt.Sprintf("%v", vals[1]))
		}
	}
	scanRem := scanLimit - scanCount
	ocrRem := ocrLimit - ocrCount
	if scanRem < 0 {
		scanRem = 0
	}
	if ocrRem < 0 {
		ocrRem = 0
	}
	return scanCount, scanRem, ocrCount, ocrRem, nil
}

func (r *RedisQuotaStore) refundOne(userID, field string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	today := time.Now().Format("2006-01-02")
	key := fmt.Sprintf("omniscan:quota:%s:%s", today, userID)

	script := redis.NewScript(`
		local current = redis.call('HGET', KEYS[1], ARGV[1])
		if current and tonumber(current) > 0 then
			return redis.call('HINCRBY', KEYS[1], ARGV[1], -1)
		end
		return 0
	`)
	return script.Run(ctx, r.Client, []string{key}, field).Err()
}

func (r *RedisQuotaStore) RefundScanQuota(userID string) error { return r.refundOne(userID, "scan_count") }

func (r *RedisQuotaStore) RefundOCRQuota(userID string) error { return r.refundOne(userID, "ocr_count") }

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
