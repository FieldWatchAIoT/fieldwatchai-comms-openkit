-- name: ListChannelsForTenant :many
SELECT id, tenant_id, name, parser_config, workflow_url, reply_policy,
       confidence_thresholds, echo_back_enabled, recall_window_seconds,
       audit_retention_years, created_at
FROM channels
WHERE tenant_id = $1
ORDER BY created_at;

-- name: GetChannelForTenant :one
SELECT id, tenant_id, name, parser_config, workflow_url, reply_policy,
       confidence_thresholds, echo_back_enabled, recall_window_seconds,
       audit_retention_years, created_at
FROM channels
WHERE id = $1 AND tenant_id = $2;

-- name: LinkAccountToChannel :one
-- Idempotent link: re-linking an already-linked account updates the routing
-- fields rather than erroring, so a console can PUT the desired state.
INSERT INTO channel_accounts (channel_id, account_id, direction, priority, routing_filter)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (channel_id, account_id) DO UPDATE
SET direction = EXCLUDED.direction,
    priority = EXCLUDED.priority,
    routing_filter = EXCLUDED.routing_filter
RETURNING channel_id, account_id, direction, priority, routing_filter;

-- name: UnlinkAccountFromChannel :execrows
DELETE FROM channel_accounts WHERE channel_id = $1 AND account_id = $2;

-- name: ListAccountLinksForChannel :many
-- The links on a channel, with enough account detail for a console to render
-- them without a second round trip.
SELECT ca.channel_id, ca.account_id, ca.direction, ca.priority, ca.routing_filter,
       a.type, a.platform_identifier, a.label, a.status
FROM channel_accounts ca
JOIN accounts a ON a.id = ca.account_id
WHERE ca.channel_id = $1
ORDER BY ca.priority DESC;

-- name: GetOutboundAccountForChannel :one
-- Resolve the account a channel sends from. Highest-priority outbound/both link
-- wins (mirrors GetInboundChannelForAccount on the receive side). Used by
-- /v1/outbound's recipient variant, which has no prior message to inherit an
-- account from.
SELECT ca.account_id, ch.tenant_id
FROM channel_accounts ca
JOIN channels ch ON ch.id = ca.channel_id
WHERE ca.channel_id = $1 AND ca.direction IN ('outbound', 'both')
ORDER BY ca.priority DESC
LIMIT 1;

-- name: GetInboundChannelForAccount :one
-- Resolve the logical channel an inbound message on this account belongs to,
-- with its config. Highest-priority inbound/both link wins. Returns no row when
-- the account isn't linked to a channel (ingest then falls back to defaults).
SELECT c.id, c.parser_config, c.confidence_thresholds, c.recall_window_seconds,
       c.workflow_url, c.reply_policy
FROM channels c
JOIN channel_accounts ca ON ca.channel_id = c.id
WHERE ca.account_id = $1 AND ca.direction IN ('inbound', 'both')
ORDER BY ca.priority DESC
LIMIT 1;
