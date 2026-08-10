//go:build integration

// Package integration holds DB-backed tests gated behind the `integration` build
// tag. They require a reachable Postgres+PostGIS and the golang-migrate CLI.
//
// Run with:
//
//	FCC_TEST_DATABASE_URL=postgres://fcc:fcc@localhost:5434/fcc?sslmode=disable \
//	  go test ./tests/integration/ -tags=integration -count=1
package integration

import (
	"context"
	"os"
	"os/exec"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5"
)

const migrationsDir = "../../migrations"

// wantTables is the full set the initial migration must create.
var wantTables = []string{
	"tenants", "accounts", "channels", "channel_accounts",
	"contacts", "contact_endpoints", "groups", "group_members",
	"messages", "workflows", "audit_log",
}

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("FCC_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("FCC_TEST_DATABASE_URL not set; skipping DB integration test")
	}
	if _, err := exec.LookPath("migrate"); err != nil {
		t.Skip("golang-migrate CLI not found in PATH; skipping")
	}
	return dsn
}

func runMigrate(t *testing.T, dsn string, args ...string) {
	t.Helper()
	full := append([]string{"-path", migrationsDir, "-database", dsn}, args...)
	out, err := exec.Command("migrate", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("migrate %v failed: %v\n%s", args, err, out)
	}
}

// resetSchema drops only this service's objects plus the migrate bookkeeping
// table, leaving the environment-managed postgis extension (and its
// spatial_ref_sys table) intact. Used to give each test a clean slate without
// fighting the pre-provisioned extension that `migrate drop` chokes on.
func resetSchema(t *testing.T, dsn string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("reset connect: %v", err)
	}
	defer conn.Close(ctx)

	for _, tbl := range wantTables {
		if _, err := conn.Exec(ctx, `DROP TABLE IF EXISTS "`+tbl+`" CASCADE`); err != nil {
			t.Fatalf("reset drop %q: %v", tbl, err)
		}
	}
	if _, err := conn.Exec(ctx, `DROP TABLE IF EXISTS schema_migrations CASCADE`); err != nil {
		t.Fatalf("reset drop schema_migrations: %v", err)
	}
}

func publicTables(t *testing.T, conn *pgx.Conn) []string {
	t.Helper()
	rows, err := conn.Query(context.Background(),
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema='public' AND table_type='BASE TABLE'`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, name)
	}
	sort.Strings(got)
	return got
}

func TestMigrationsCreateFullSchema(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()

	// Start from a clean slate, then apply up; ensure full teardown after.
	resetSchema(t, dsn)
	runMigrate(t, dsn, "up")
	t.Cleanup(func() { resetSchema(t, dsn) })

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	got := publicTables(t, conn)
	present := make(map[string]bool, len(got))
	for _, n := range got {
		present[n] = true
	}
	for _, want := range wantTables {
		if !present[want] {
			t.Errorf("missing table %q (got: %v)", want, got)
		}
	}

	// PostGIS must be enabled (messages.body_location is GEOGRAPHY).
	var ext string
	err = conn.QueryRow(ctx, `SELECT extname FROM pg_extension WHERE extname='postgis'`).Scan(&ext)
	if err != nil {
		t.Errorf("postgis extension not present: %v", err)
	}

	// messages must enforce idempotency on (account_id, platform_message_id).
	var n int
	err = conn.QueryRow(ctx, `
		SELECT count(*) FROM pg_indexes
		WHERE tablename='messages' AND indexdef ILIKE '%UNIQUE%account_id%platform_message_id%'`).Scan(&n)
	if err != nil {
		t.Fatalf("index introspection: %v", err)
	}
	if n == 0 {
		t.Error("missing UNIQUE(account_id, platform_message_id) on messages")
	}
}

func TestMigrationsDownIsClean(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()

	resetSchema(t, dsn)
	runMigrate(t, dsn, "up")
	runMigrate(t, dsn, "down", "-all")

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	for _, tbl := range wantTables {
		var exists bool
		err := conn.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			 WHERE table_schema='public' AND table_name=$1)`, tbl).Scan(&exists)
		if err != nil {
			t.Fatalf("exists check %q: %v", tbl, err)
		}
		if exists {
			t.Errorf("table %q still present after down", tbl)
		}
	}
}
