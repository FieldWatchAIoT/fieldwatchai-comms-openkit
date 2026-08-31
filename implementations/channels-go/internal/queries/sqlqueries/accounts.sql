-- name: CreateAccount :one
INSERT INTO accounts (
  id, tenant_id, type, owner_type, label, platform_identifier,
  credentials_encrypted, capabilities, status, created_at
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
)
RETURNING *;

-- name: GetAccountForTenant :one
SELECT * FROM accounts
WHERE id = $1 AND tenant_id = $2;

-- name: ListAccountsForTenant :many
SELECT * FROM accounts
WHERE tenant_id = $1
ORDER BY created_at DESC;

-- name: ListAccountsWithRoutingForTenant :many
-- Accounts plus the channel each one's INBOUND traffic actually resolves to —
-- computed with the same rule ingest uses (highest-priority inbound/both link).
-- A NULL inbound_channel_id means messages to that account reach no consumer:
-- they fall back to the service defaults, land on clarify, and dispatch nothing.
-- Surfacing it here is what stops that state from being invisible.
SELECT a.id, a.tenant_id, a.type, a.owner_type, a.label, a.platform_identifier,
       a.capabilities, a.status, a.created_at,
       -- Emitted as text, '' when unlinked: sqlc infers a bare lateral column as
       -- NOT NULL and would fail the scan on every unrouted account. The service
       -- converts '' back to a nil channel id.
       COALESCE(inbound.id::text, '')::text AS inbound_channel_id
FROM accounts a
LEFT JOIN LATERAL (
  SELECT ch.id
  FROM channels ch
  JOIN channel_accounts ca ON ca.channel_id = ch.id
  WHERE ca.account_id = a.id AND ca.direction IN ('inbound', 'both')
  ORDER BY ca.priority DESC
  LIMIT 1
) inbound ON TRUE
WHERE a.tenant_id = $1
ORDER BY a.created_at DESC;

-- name: UpdateAccount :one
UPDATE accounts
SET label = $3,
    status = $4,
    capabilities = $5,
    credentials_encrypted = $6
WHERE id = $1 AND tenant_id = $2
RETURNING *;

-- name: DeleteAccount :execrows
DELETE FROM accounts
WHERE id = $1 AND tenant_id = $2;

-- name: LookupAccount :one
-- Resolve an account by platform type + identifier (used by the webhook's
-- account lookup). Returns only the ids — never credentials.
--
-- Only an active account resolves. Suspending an account is the documented way
-- to stop traffic from an abusive or compromised sender, and until this filter
-- existed the status column was decorative: a suspended account kept accepting
-- messages exactly as before, so an operator who suspended one would reasonably
-- believe they had stopped it. A suspended account now looks unregistered to
-- the webhook, which acknowledges and drops.
SELECT id, tenant_id FROM accounts
WHERE type = $1 AND platform_identifier = $2 AND status = 'active';

-- name: GetAccountByID :one
-- Resolve an account by id without a tenant filter; the row carries tenant_id.
-- Used by /v1/ingest, which learns the tenant from the account.
SELECT * FROM accounts WHERE id = $1;
