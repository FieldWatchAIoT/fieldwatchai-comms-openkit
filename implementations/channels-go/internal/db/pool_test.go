//go:build integration

package db

import (
	"context"
	"os"
	"runtime"
	"testing"
)

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("FCC_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("FCC_TEST_DATABASE_URL not set; skipping DB integration test")
	}
	return dsn
}

func TestNewPoolConnectsAndPings(t *testing.T) {
	ctx := context.Background()
	pool, err := NewPool(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	var one int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if one != 1 {
		t.Fatalf("SELECT 1 = %d, want 1", one)
	}
}

func TestNewPoolSetsMaxConns(t *testing.T) {
	ctx := context.Background()
	pool, err := NewPool(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	want := int32(4 * runtime.NumCPU())
	if got := pool.Config().MaxConns; got != want {
		t.Errorf("MaxConns = %d, want %d", got, want)
	}
}

func TestNewPoolRejectsBadDSN(t *testing.T) {
	_, err := NewPool(context.Background(), "not a dsn ::: %%%")
	if err == nil {
		t.Fatal("NewPool with garbage DSN = nil error, want error")
	}
}
