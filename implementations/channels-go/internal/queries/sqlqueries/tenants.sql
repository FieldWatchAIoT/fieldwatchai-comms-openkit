-- name: CreateTenant :one
-- Bootstrap a tenant. Tenancy is provisioned out of band in FieldWatch's own
-- deployment, but an adopter starting from an empty database has no other way
-- in: accounts.tenant_id is a foreign key onto this table, so without a tenant
-- row every POST /v1/accounts fails the constraint and returns a bare
-- internal_error. Idempotent on id so a setup script can be re-run.
INSERT INTO tenants (id, name, plan, created_at, settings)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO NOTHING
RETURNING id, name, plan, created_at, settings;

-- name: GetTenant :one
SELECT id, name, plan, created_at, settings FROM tenants WHERE id = $1;

-- name: ListTenants :many
SELECT id, name, plan, created_at, settings FROM tenants ORDER BY created_at;
