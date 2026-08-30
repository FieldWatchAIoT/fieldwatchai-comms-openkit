#!/usr/bin/env bash
# Creates a SEPARATE database for the integration tests.
#
# The integration suite is destructive by design — TestMigrationsDownIsClean
# runs `migrate down`, which drops every table — so it must never be pointed at
# a database holding data anyone cares about. Giving it its own database means
# `make test-integration` cannot wipe the demo data in `openkit`.
#
# Runs once, on a fresh Postgres volume, via docker-entrypoint-initdb.d.
set -euo pipefail

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-SQL
	CREATE DATABASE ${POSTGRES_DB}_test;
SQL

# PostGIS lives per-database, and messages.body_location is a GEOGRAPHY column,
# so the test database needs the extension too. CREATE EXTENSION is privileged;
# it works here because initdb scripts run as the superuser, which is also why
# the app's own migrations deliberately do not attempt it.
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "${POSTGRES_DB}_test" <<-SQL
	CREATE EXTENSION IF NOT EXISTS postgis;
SQL

echo "[init] created ${POSTGRES_DB}_test for the integration suite"
