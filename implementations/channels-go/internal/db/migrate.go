package db

import (
	"errors"
	"fmt"

	"github.com/FieldWatchAIoT/fieldwatchai-comms-openkit/implementations/channels-go/migrations"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// RunMigrations applies all up migrations embedded in the binary against dsn.
// It is idempotent (a no-op when the schema is already current) and safe to run
// on every boot — golang-migrate takes an advisory lock so concurrent task
// starts serialize.
//
// The connection is opened via pgx (using SanitizeDSN), NOT golang-migrate's
// URL parser: Aurora's auto-generated passwords contain characters net/url
// rejects ("invalid userinfo"), so we hand golang-migrate a ready *sql.DB
// instead of a URL.
func RunMigrations(dsn string) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}

	connCfg, err := pgx.ParseConfig(SanitizeDSN(dsn))
	if err != nil {
		return fmt.Errorf("parse migration dsn: %w", err)
	}
	sqlDB := stdlib.OpenDB(*connCfg)
	defer sqlDB.Close()

	driver, err := postgres.WithInstance(sqlDB, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("init migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		return fmt.Errorf("init migrator: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
