package object

import (
	"context"
	"io"
	"time"

	"macocr/proxy/domain"
)

type Repository = domain.ObjectRepository

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Put(ctx context.Context, key string, body io.Reader, contentType string) error {
	return s.repo.Put(ctx, key, body, contentType)
}

func (s *Service) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.repo.Get(ctx, key)
}

func (s *Service) Exists(ctx context.Context, key string) (bool, error) {
	return s.repo.Exists(ctx, key)
}

func (s *Service) PresignGetURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return s.repo.PresignGetURL(ctx, key, ttl)
}

func (s *Service) Ready(ctx context.Context) error {
	return s.repo.Ping(ctx)
}
