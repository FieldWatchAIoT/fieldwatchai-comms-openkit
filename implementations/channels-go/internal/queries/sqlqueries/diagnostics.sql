-- Queries behind GET /v1/diagnostics — the "why is nothing happening?" check.
--
-- The failure these exist to surface is silent by construction: an account with
-- no inbound channel link still accepts and stores messages, and forwards them
-- nowhere. Nothing errors, the webhook returns 200, and the only evidence is one
-- warning line in the channels container log. These turn that into a sentence.

-- name: CountAccountsForTenant :one
SELECT count(*) FROM accounts WHERE tenant_id = $1;

-- name: CountChannelsForTenant :one
SELECT count(*) FROM channels WHERE tenant_id = $1;

-- name: CountContactsForTenant :one
SELECT count(*) FROM contacts WHERE tenant_id = $1;

-- name: CountMessagesForTenant :one
SELECT count(*) FROM messages WHERE tenant_id = $1;

-- name: ListUnroutableAccounts :many
-- Accounts with no inbound-capable channel link. Every message arriving on one
-- of these is stored and forwarded nowhere.
SELECT a.id, a.type, a.platform_identifier, a.label
FROM accounts a
WHERE a.tenant_id = $1
  AND NOT EXISTS (
    SELECT 1 FROM channel_accounts ca
    WHERE ca.account_id = a.id AND ca.direction IN ('inbound', 'both')
  )
ORDER BY a.created_at;

-- name: ListChannelsWithoutWorkflow :many
-- Channels that resolve inbound traffic but have nowhere to forward it to. Less
-- severe than an unroutable account (parsing and storage still apply) but the
-- consumer product never hears about the message.
SELECT id, name FROM channels
WHERE tenant_id = $1 AND (workflow_url IS NULL OR workflow_url = '')
ORDER BY created_at;
