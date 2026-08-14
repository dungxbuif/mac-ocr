package tests

import (
	"context"
	"errors"
	"testing"

	"macocr/proxy/domain"
	"macocr/proxy/internal/usecase/system"
)

var errBoom = errors.New("boom")

type stubChecker struct {
	err error
}

func (s stubChecker) Ready(context.Context) error { return s.err }

func TestReadyAllHealthy(t *testing.T) {
	svc := system.NewService(stubChecker{}, stubChecker{}, stubChecker{})
	if err := svc.Ready(context.Background()); err != nil {
		t.Fatalf("expected ready, got %v", err)
	}
}

func TestReadyAggregatesFailures(t *testing.T) {
	svc := system.NewService(
		stubChecker{},
		stubChecker{err: domain.ErrStorageUnavailable},
		stubChecker{err: errBoom},
	)
	err := svc.Ready(context.Background())
	if err == nil {
		t.Fatal("expected aggregated error")
	}
	joined := errors.Join(err)
	if !errors.Is(joined, domain.ErrStorageUnavailable) {
		t.Fatalf("expected ErrStorageUnavailable in join, got %v", joined)
	}
	if !errors.Is(joined, errBoom) {
		t.Fatalf("expected boom in join, got %v", joined)
	}
}

func TestReadyNoCheckers(t *testing.T) {
	svc := system.NewService()
	if err := svc.Ready(context.Background()); err != nil {
		t.Fatalf("expected nil with no checkers, got %v", err)
	}
}
