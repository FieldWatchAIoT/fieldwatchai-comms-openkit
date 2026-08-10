// Package migrations embeds the SQL migration files so the server can apply
// them on boot (golang-migrate via an iofs source). The .sql files remain
// usable by the `migrate` CLI (Makefile) and by sqlc's schema glob — adding
// this Go file to the directory changes neither.
package migrations

import "embed"

// FS holds the NNNNNN_name.{up,down}.sql migration files.
//
//go:embed *.sql
var FS embed.FS
