package db

import (
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestSanitizeDSNHandlesNetURLHostilePassword guards the bug that crashed the
// first dev deploy: Aurora passwords contain characters net/url rejects
// ("invalid userinfo"). The migrator must open the DB via pgx + SanitizeDSN
// (which falls back to keyword/value form), never net/url URL parsing.
// Synthetic hostile password here — NOT a real secret.
func TestSanitizeDSNHandlesNetURLHostilePassword(t *testing.T) {
	const pw = `p%us+w/rd:^$;` // %us is not valid percent-encoding -> net/url errors
	raw := `postgres://svc_user:` + pw + `@db.example.internal:5432/appdb?sslmode=require`

	cfg, err := pgx.ParseConfig(SanitizeDSN(raw))
	if err != nil {
		t.Fatalf("pgx.ParseConfig(SanitizeDSN(hostile)) = %v, want nil", err)
	}
	if cfg.Password != pw {
		t.Errorf("password mangled: got %q, want %q", cfg.Password, pw)
	}
	if cfg.Host != "db.example.internal" {
		t.Errorf("host = %q", cfg.Host)
	}
	if cfg.Database != "appdb" {
		t.Errorf("database = %q", cfg.Database)
	}
}
