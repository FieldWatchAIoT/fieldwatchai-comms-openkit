//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/accounts"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/crypto"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/db"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// freshSchemaWithTenant resets to a clean migrated schema and inserts a tenant
// (accounts.tenant_id is a FK), returning the pool and tenant id.
func freshSchemaWithTenant(t *testing.T) (*pgxpool.Pool, uuid.UUID) {
	t.Helper()
	dsn := testDSN(t)
	resetSchema(t, dsn)
	runMigrate(t, dsn, "up")
	t.Cleanup(func() { resetSchema(t, dsn) })

	pool, err := db.NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	tenant := uuid.New()
	_, err = pool.Exec(context.Background(),
		`INSERT INTO tenants (id, name, plan, created_at) VALUES ($1,$2,$3,$4)`,
		tenant, "Test Tenant", "pro", time.Now().UTC())
	if err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return pool, tenant
}

func TestAccountsFullStack(t *testing.T) {
	ctx := context.Background()
	pool, tenant := freshSchemaWithTenant(t)

	enc, err := crypto.NewLocalAES("MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDA=")
	if err != nil {
		t.Fatalf("enc: %v", err)
	}
	svc := accounts.NewService(goqueries.New(pool), enc, time.Now, uuid.New)

	// Create
	acc, err := svc.Create(ctx, accounts.CreateInput{
		TenantID:           tenant,
		Type:               "whatsapp",
		OwnerType:          "platform",
		Label:              "WhatsApp #1",
		PlatformIdentifier: "179557",
		Status:             "active",
		Capabilities:       []string{"inbound", "outbound"},
		Credentials:        []byte("ultramsg-token"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Credentials must be encrypted at rest (verify the raw DB column).
	var stored []byte
	if err := pool.QueryRow(ctx, `SELECT credentials_encrypted FROM accounts WHERE id=$1`, acc.ID).Scan(&stored); err != nil {
		t.Fatalf("read raw creds: %v", err)
	}
	if len(stored) == 0 || string(stored) == "ultramsg-token" {
		t.Fatalf("credentials stored in plaintext: %q", stored)
	}
	if pt, _ := enc.Decrypt(ctx, stored); string(pt) != "ultramsg-token" {
		t.Fatalf("stored ciphertext does not decrypt to plaintext")
	}

	// Get + List
	got, err := svc.Get(ctx, acc.ID, tenant)
	if err != nil || got.Label != "WhatsApp #1" {
		t.Fatalf("Get: %v acc=%+v", err, got)
	}
	list, err := svc.List(ctx, tenant)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v len=%d", err, len(list))
	}

	// Cross-tenant isolation: another tenant cannot see it.
	if _, err := svc.Get(ctx, acc.ID, uuid.New()); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("cross-tenant Get = %v, want ErrNotFound", err)
	}

	// Update label + rotate credentials.
	newLabel := "WhatsApp #1 (Resilience)"
	if _, err := svc.Update(ctx, acc.ID, tenant, accounts.UpdateInput{
		Label:       &newLabel,
		Credentials: []byte("rotated-token"),
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = svc.Get(ctx, acc.ID, tenant)
	if got.Label != newLabel {
		t.Errorf("label not updated: %q", got.Label)
	}
	if err := pool.QueryRow(ctx, `SELECT credentials_encrypted FROM accounts WHERE id=$1`, acc.ID).Scan(&stored); err != nil {
		t.Fatalf("read rotated creds: %v", err)
	}
	if pt, _ := enc.Decrypt(ctx, stored); string(pt) != "rotated-token" {
		t.Errorf("credentials not rotated")
	}

	// Lookup by type + identifier resolves to the id pair.
	res, err := svc.Lookup(ctx, "whatsapp", "179557")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if res.AccountID != acc.ID || res.TenantID != tenant {
		t.Errorf("Lookup = %+v, want %v/%v", res, acc.ID, tenant)
	}
	if _, err := svc.Lookup(ctx, "whatsapp", "does-not-exist"); !errors.Is(err, accounts.ErrNotFound) {
		t.Errorf("Lookup miss = %v, want ErrNotFound", err)
	}

	// Delete + confirm gone.
	if err := svc.Delete(ctx, acc.ID, tenant); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(ctx, acc.ID, tenant); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
}
