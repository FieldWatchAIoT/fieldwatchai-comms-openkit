//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/db"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
)

// TestGeneratedQueriesRunAgainstDB proves the sqlc-generated code executes
// against a real pgx pool end to end.
func TestGeneratedQueriesRunAgainstDB(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()

	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	q := goqueries.New(pool)
	ok, err := q.Ping(ctx)
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if ok != 1 {
		t.Fatalf("Ping = %d, want 1", ok)
	}
}
