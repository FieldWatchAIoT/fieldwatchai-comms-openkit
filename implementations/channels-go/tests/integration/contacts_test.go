//go:build integration

package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/contacts"
	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/internal/queries/goqueries"
	"github.com/google/uuid"
)

func TestContactsBulkImport(t *testing.T) {
	ctx := context.Background()
	pool, tenant := freshSchemaWithTenant(t)
	svc := contacts.NewService(goqueries.New(pool), time.Now, uuid.New)

	// Pre-existing contact "42" so the CSV row for it collides.
	if _, err := svc.Create(ctx, contacts.CreateInput{TenantID: tenant, ShortID: "42", DisplayName: "Existing"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	csv := strings.Join([]string{
		"short_id,display_name,role",
		"42,Dup,shelter",   // collision
		"HMB,Hope Town,shelter",
		",NoID,staff",       // missing short_id
		"EOC,Ops,eoc",
	}, "\n")

	res, err := svc.BulkImport(ctx, tenant, strings.NewReader(csv))
	if err != nil {
		t.Fatalf("BulkImport: %v", err)
	}
	if res.Created != 2 {
		t.Errorf("Created = %d, want 2 (HMB, EOC)", res.Created)
	}
	if len(res.Errors) != 2 {
		t.Errorf("Errors = %d (%+v), want 2 (collision + missing)", len(res.Errors), res.Errors)
	}

	// The two valid rows are actually persisted.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM contacts WHERE tenant_id=$1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 { // existing 42 + HMB + EOC
		t.Errorf("contact count = %d, want 3", n)
	}
}

func TestContactsFullStack(t *testing.T) {
	ctx := context.Background()
	pool, tenant := freshSchemaWithTenant(t)
	svc := contacts.NewService(goqueries.New(pool), time.Now, uuid.New)

	aoi := "abaco"
	c, err := svc.Create(ctx, contacts.CreateInput{
		TenantID: tenant, ShortID: "42", DisplayName: "Marsh Harbour Shelter", AOIID: &aoi,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.AOIID == nil || *c.AOIID != "abaco" {
		t.Errorf("aoi not persisted: %+v", c.AOIID)
	}

	// Collision: same short_id in same tenant.
	if _, err := svc.Create(ctx, contacts.CreateInput{TenantID: tenant, ShortID: "42", DisplayName: "Dup"}); !errors.Is(err, contacts.ErrConflict) {
		t.Fatalf("duplicate short_id = %v, want ErrConflict", err)
	}

	// Same short_id in a DIFFERENT tenant is allowed.
	other := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO tenants (id,name,plan,created_at) VALUES ($1,'O','pro',$2)`, other, time.Now().UTC()); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := svc.Create(ctx, contacts.CreateInput{TenantID: other, ShortID: "42", DisplayName: "Other 42"}); err != nil {
		t.Fatalf("cross-tenant same short_id should be allowed: %v", err)
	}

	// short_id_check: exact exists + near-miss suggestion.
	if _, err := svc.Create(ctx, contacts.CreateInput{TenantID: tenant, ShortID: "43", DisplayName: "Neighbor"}); err != nil {
		t.Fatalf("Create 43: %v", err)
	}
	chk, _ := svc.ShortIDCheck(ctx, tenant, "42")
	if !chk.Exists {
		t.Error("42 should exist")
	}
	chk2, _ := svc.ShortIDCheck(ctx, tenant, "4")
	if chk2.Exists {
		t.Error("4 should not exist")
	}
	if len(chk2.Suggestions) == 0 {
		t.Error("expected near-miss suggestions for 4")
	}

	// Update + delete.
	newName := "Marsh Harbour EOC"
	if _, err := svc.Update(ctx, c.ID, tenant, contacts.UpdateInput{DisplayName: &newName}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := svc.Get(ctx, c.ID, tenant)
	if got.DisplayName != newName {
		t.Errorf("name not updated: %q", got.DisplayName)
	}
	if err := svc.Delete(ctx, c.ID, tenant); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(ctx, c.ID, tenant); !errors.Is(err, contacts.ErrNotFound) {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
}
