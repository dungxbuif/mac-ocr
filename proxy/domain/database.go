package domain

import "context"

type DatabaseRepository interface {
	Ping(ctx context.Context) error
	Close() error
}
