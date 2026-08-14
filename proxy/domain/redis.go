package domain

import "context"

type RedisRepository interface {
	Ping(ctx context.Context) error
}
