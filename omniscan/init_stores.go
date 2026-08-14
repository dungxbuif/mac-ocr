package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"omniscan/storage"
)

// initPostgres opens quota + session pools and returns them. The quota store's
// *PostgresQuotaStore handle is passed up so the bot can run event-driven
// UpsertUser calls without re-opening a pool.
func initPostgres(databaseURL string) (storage.QuotaStore, storage.SessionStore, *storage.PostgresQuotaStore, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pqs, err := storage.NewPostgresQuotaStore(ctx, databaseURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("postgres quota store: %w", err)
	}
	pss, err := storage.NewPostgresSessionStore(ctx, databaseURL)
	if err != nil {
		pqs.Close()
		return nil, nil, nil, fmt.Errorf("postgres session store: %w", err)
	}
	return pqs, pss, pqs, nil
}

func initSQLite() (storage.QuotaStore, storage.SessionStore, error) {
	sq, err := storage.NewSQLiteQuotaStore("omniscan.db")
	if err != nil {
		return nil, nil, fmt.Errorf("sqlite quota store: %w", err)
	}
	ss, err := storage.NewSQLiteSessionStore("omniscan_sessions.db")
	if err != nil {
		sq.Close()
		return nil, nil, fmt.Errorf("sqlite session store: %w", err)
	}
	return sq, ss, nil
}

// redactURL strips credentials from a database/redis URL for safe logging.
// "postgres://user:pass@host/db" -> "postgres://***@host/db".
func redactURL(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		scheme := u[:i+3]
		rest := u[i+3:]
		if at := strings.Index(rest, "@"); at >= 0 {
			return scheme + "***@" + rest[at+1:]
		}
	}
	return u
}
