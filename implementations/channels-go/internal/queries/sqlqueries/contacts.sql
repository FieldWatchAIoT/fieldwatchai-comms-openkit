-- name: CreateContact :one
INSERT INTO contacts (
  id, tenant_id, short_id, display_name, aoi_id, role, status, metadata, created_at, updated_at
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $9
)
RETURNING *;

-- name: GetContactForTenant :one
SELECT * FROM contacts WHERE id = $1 AND tenant_id = $2;

-- name: GetContactByShortID :one
SELECT * FROM contacts WHERE tenant_id = $1 AND short_id = $2;

-- name: ListContactsForTenant :many
SELECT * FROM contacts WHERE tenant_id = $1 ORDER BY short_id;

-- name: ListContactShortIDs :many
-- Lightweight projection for the resolver candidate set + short_id_check.
SELECT id, short_id, display_name FROM contacts WHERE tenant_id = $1;

-- name: UpdateContact :one
UPDATE contacts
SET display_name = $3,
    aoi_id = $4,
    role = $5,
    status = $6,
    metadata = $7,
    updated_at = $8
WHERE id = $1 AND tenant_id = $2
RETURNING *;

-- name: DeleteContact :execrows
DELETE FROM contacts WHERE id = $1 AND tenant_id = $2;

-- name: GetOutboundEndpointForContact :one
-- Resolve where to send a message addressed to a contact by id: the
-- highest-priority endpoint that can send, plus the account to send it from.
-- Optionally constrained to one channel (pass NULL for "any channel"). Used by
-- /v1/outbound's recipient variant — unlike the reply path there is no prior
-- message to inherit the endpoint/account from.
SELECT ce.endpoint, ce.account_id, ce.channel_id, c.tenant_id
FROM contact_endpoints ce
JOIN contacts c ON c.id = ce.contact_id
WHERE ce.contact_id = $1
  AND 'outbound' = ANY(ce.capabilities)
  AND (sqlc.narg('channel_id')::uuid IS NULL OR ce.channel_id = sqlc.narg('channel_id')::uuid)
ORDER BY ce.priority DESC
LIMIT 1;
