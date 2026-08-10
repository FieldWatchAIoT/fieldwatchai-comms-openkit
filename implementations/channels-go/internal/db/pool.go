// Package db provides the pgx connection pool and DSN handling for
// comms-channels, following the big-data-api / news-api conventions.
package db

import (
	"context"
	"fmt"
	"runtime"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool builds a pgx connection pool from a DSN (URL or keyword/value form),
// sizing MaxConns to 4×NumCPU (the FieldWatch convention) and verifying
// connectivity with a Ping before returning.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(SanitizeDSN(dsn))
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}
	cfg.MaxConns = int32(4 * runtime.NumCPU())

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}
