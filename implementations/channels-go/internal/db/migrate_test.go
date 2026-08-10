//go:build integration

package db

import (
	"context"
	"testing"
)

func TestRunMigrationsIsIdempotent(t *testing.T) {
	dsn := testDSN(t)

	// Applying twice must both succeed (second is a no-op).
	if err := RunMigrations(dsn); err != nil {
		t.Fatalf("RunMigrations #1: %v", err)
	}
	if err := RunMigrations(dsn); err != nil {
		t.Fatalf("RunMigrations #2 (should be no-op): %v", err)
	}

	pool, err := NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	var exists bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		 WHERE table_schema='public' AND table_name='messages')`).Scan(&exists); err != nil {
		t.Fatalf("table check: %v", err)
	}
	if !exists {
		t.Error("expected messages table after RunMigrations")
	}
}
