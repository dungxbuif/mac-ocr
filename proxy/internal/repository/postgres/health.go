// Package postgres provides a PostgreSQL adapter used for readiness probes
// and future persistence. It implements the domain.DatabaseRepository
// contract.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"macocr/proxy/domain"
)

// Repository wraps a pgx connection pool.
type Repository struct {
	pool *pgxpool.Pool
}

// New builds a PostgreSQL Repository from a connection URL.
func New(ctx context.Context, url string) (*Repository, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	return &Repository{pool: pool}, nil
}

// Pool exposes the underlying pgx pool for building focused repositories.
func (r *Repository) Pool() *pgxpool.Pool { return r.pool }

// Close releases the connection pool.
func (r *Repository) Close() error {
	r.pool.Close()
	return nil
}

// Ping verifies connectivity to the database.
func (r *Repository) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := r.pool.Ping(ctx); err != nil {
		return fmt.Errorf("%w: %s", domain.ErrStorageUnavailable, err)
	}
	return nil
}

// Ready verifies connectivity; used by the system readiness probe.
func (r *Repository) Ready(ctx context.Context) error {
	return r.Ping(ctx)
}
