package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"macocr/proxy/domain"
)

type Repository struct {
	client *redis.Client
}

func New(url string) (*Repository, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	return &Repository{client: redis.NewClient(opt)}, nil
}

func (r *Repository) Close() error {
	return r.client.Close()
}

func (r *Repository) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := r.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("%w: %s", domain.ErrStorageUnavailable, err)
	}
	return nil
}

func (r *Repository) Ready(ctx context.Context) error {
	return r.Ping(ctx)
}

func (r *Repository) Allow(ctx context.Context, id string, limit int) (bool, error) {
	if limit <= 0 {
		return true, nil
	}
	window := time.Now().UTC().Format("200601021504")
	key := "ratelimit:" + id + ":" + window

	pipe := r.client.TxPipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, 2*time.Minute)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("rate limit incr: %w", err)
	}
	return incr.Val() <= int64(limit), nil
}

const (
	accountConfigTTL = 5 * time.Minute
	apiKeyTTL        = 5 * time.Minute
)

func apiKeyCacheKey(hash string) string { return "ocr:api-key:" + hash }
func apiKeyUserIndexKey(userID int64) string {
	return fmt.Sprintf("ocr:api-key-user:%d", userID)
}

func (r *Repository) GetAPIKey(ctx context.Context, hash string) (*domain.ApiKey, error) {
	data, err := r.client.Get(ctx, apiKeyCacheKey(hash)).Bytes()
	if err == redis.Nil {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("%w: read API key cache: %v", domain.ErrStorageUnavailable, err)
	}
	var key domain.ApiKey
	if err := json.Unmarshal(data, &key); err != nil {
		_ = r.client.Del(ctx, apiKeyCacheKey(hash)).Err()
		return nil, domain.ErrNotFound
	}
	if key.ID <= 0 || key.UserID <= 0 || key.RevokedAt != nil {
		_ = r.client.Del(ctx, apiKeyCacheKey(hash)).Err()
		return nil, domain.ErrNotFound
	}
	return &key, nil
}

func (r *Repository) SetAPIKey(ctx context.Context, hash string, key *domain.ApiKey) error {
	if hash == "" || key == nil || key.ID <= 0 || key.UserID <= 0 || key.RevokedAt != nil {
		return fmt.Errorf("cache API key: invalid active key")
	}
	copy := *key
	copy.KeyHash = ""
	data, err := json.Marshal(&copy)
	if err != nil {
		return fmt.Errorf("encode API key cache: %w", err)
	}
	cacheKey := apiKeyCacheKey(hash)
	indexKey := apiKeyUserIndexKey(key.UserID)
	pipe := r.client.TxPipeline()
	pipe.Set(ctx, cacheKey, data, apiKeyTTL)
	pipe.SAdd(ctx, indexKey, cacheKey)
	pipe.Expire(ctx, indexKey, apiKeyTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("%w: write API key cache: %v", domain.ErrStorageUnavailable, err)
	}
	return nil
}

func (r *Repository) DeleteAPIKeysByUser(ctx context.Context, userID int64) error {
	indexKey := apiKeyUserIndexKey(userID)
	keys, err := r.client.SMembers(ctx, indexKey).Result()
	if err != nil && err != redis.Nil {
		return fmt.Errorf("%w: read API key cache index: %v", domain.ErrStorageUnavailable, err)
	}
	keys = append(keys, indexKey)
	if err := r.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("%w: invalidate API key cache: %v", domain.ErrStorageUnavailable, err)
	}
	return nil
}

func (r *Repository) GetAccountConfig(ctx context.Context, userID int64) (*domain.AccountConfig, error) {
	data, err := r.client.Get(ctx, fmt.Sprintf("ocr:account-config:%d", userID)).Bytes()
	if err == redis.Nil {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("%w: read account config cache: %v", domain.ErrStorageUnavailable, err)
	}
	var cfg domain.AccountConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		_ = r.DeleteAccountConfig(ctx, userID)
		return nil, domain.ErrNotFound
	}
	return &cfg, nil
}

func (r *Repository) SetAccountConfig(ctx context.Context, cfg *domain.AccountConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode account config cache: %w", err)
	}
	if err := r.client.Set(ctx, fmt.Sprintf("ocr:account-config:%d", cfg.UserID), data, accountConfigTTL).Err(); err != nil {
		return fmt.Errorf("%w: write account config cache: %v", domain.ErrStorageUnavailable, err)
	}
	return nil
}

func (r *Repository) DeleteAccountConfig(ctx context.Context, userID int64) error {
	if err := r.client.Del(ctx, fmt.Sprintf("ocr:account-config:%d", userID)).Err(); err != nil {
		return fmt.Errorf("%w: invalidate account config cache: %v", domain.ErrStorageUnavailable, err)
	}
	return nil
}

func (r *Repository) SetResult(ctx context.Context, documentID string, result *domain.OCRResult, ttl time.Duration) error {
	if result == nil || ttl <= 0 {
		return fmt.Errorf("cache OCR result: invalid result or TTL")
	}
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode OCR result: %w", err)
	}
	if err := r.client.Set(ctx, "ocr:result:"+documentID, data, ttl).Err(); err != nil {
		return fmt.Errorf("%w: write OCR result cache: %v", domain.ErrStorageUnavailable, err)
	}
	return nil
}

func (r *Repository) GetResult(ctx context.Context, documentID string) (*domain.OCRResult, error) {
	data, err := r.client.Get(ctx, "ocr:result:"+documentID).Bytes()
	if err == redis.Nil {
		return nil, domain.ErrResultExpired
	}
	if err != nil {
		return nil, fmt.Errorf("%w: read OCR result cache: %v", domain.ErrStorageUnavailable, err)
	}
	var result domain.OCRResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("%w: decode OCR result cache: %v", domain.ErrStorageUnavailable, err)
	}
	return &result, nil
}

func (r *Repository) DeleteResult(ctx context.Context, documentID string) error {
	if err := r.client.Del(ctx, "ocr:result:"+documentID).Err(); err != nil {
		return fmt.Errorf("%w: delete OCR result cache: %v", domain.ErrStorageUnavailable, err)
	}
	return nil
}

func (r *Repository) SetSession(ctx context.Context, token string, data []byte, ttl time.Duration) error {
	return r.client.Set(ctx, "ocr:session:"+token, data, ttl).Err()
}

func (r *Repository) GetSession(ctx context.Context, token string) ([]byte, error) {
	data, err := r.client.Get(ctx, "ocr:session:"+token).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	return data, err
}

func (r *Repository) DeleteSession(ctx context.Context, token string) error {
	return r.client.Del(ctx, "ocr:session:"+token).Err()
}
