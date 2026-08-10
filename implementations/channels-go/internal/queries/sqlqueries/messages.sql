-- name: CreateInboundMessage :one
-- Persist an inbound message. Idempotent on (account_id, platform_message_id):
-- a duplicate inserts nothing and returns no row (the handler then treats it as
-- a replay). parsed carries the structured parse+resolution; policy_action is
-- the gate's decision (confidence lives inside parsed).
-- body_location takes EWKT ('SRID=4326;POINT(lng lat)'); '' means the sender
-- provided no location and stores NULL. Passing text and casting keeps the
-- generated param a plain string instead of an opaque geography type.
INSERT INTO messages (
  id, tenant_id, account_id, channel_id, direction, sender_endpoint, body_text,
  body_attachments, parsed, policy_action, platform_message_id, raw_payload,
  received_at, processed_at, body_location
) VALUES (
  $1, $2, $3, $4, 'inbound', $5, $6, $7, $8, $9, $10, $11, $12, $13,
  NULLIF(sqlc.arg(body_location)::text, '')::geography
)
ON CONFLICT (account_id, platform_message_id) DO NOTHING
RETURNING id;

-- name: GetMessageIDByPlatformID :one
-- Resolve an existing message id for the replay path.
SELECT id FROM messages
WHERE account_id = $1 AND platform_message_id = $2;

-- name: CreateOutboundMessage :one
-- Persist an outbound message (e.g. an echo-back), linked to the inbound it
-- replies to.
INSERT INTO messages (
  id, tenant_id, account_id, direction, recipient_contact_id, body_text,
  policy_action, in_reply_to_message_id, platform_message_id, raw_payload,
  received_at, processed_at
) VALUES (
  $1, $2, $3, 'outbound', $4, $5, $6, $7, $8, '{}', $9, $9
)
RETURNING id;

-- name: FindRecentEchoForSender :one
-- The most recent inbound message from this sender on this account that was
-- echoed back and is still inside the recall window ($3 = cutoff). Used to
-- resolve an "OOPS" recall.
SELECT id, body_text FROM messages
WHERE account_id = $1
  AND sender_endpoint = $2
  AND direction = 'inbound'
  AND policy_action = 'echo_back'
  AND received_at >= $3
ORDER BY received_at DESC
LIMIT 1;

-- name: MarkMessageRecalled :exec
UPDATE messages SET policy_action = 'recalled' WHERE id = $1;

-- name: MarkWorkflowFired :exec
UPDATE messages SET workflow_fired = TRUE WHERE id = $1;

-- name: ListUnfiredForwards :many
-- Inbound messages that should have reached a consumer and didn't:
-- workflow_fired is still false, the channel has a workflow_url, and the policy
-- gate decided to route them ('routed' on passthrough, 'execute' on structured).
-- Everything needed to rebuild the forward payload is selected here, so a
-- replay reconstructs from stored state rather than re-deriving it.
-- Oldest first, so a consumer sees a replayed batch in the order it happened.
SELECT m.id, m.tenant_id, m.account_id, m.channel_id, m.sender_endpoint,
       m.body_text, m.body_attachments, m.parsed, m.received_at,
       -- GeoJSON rather than the raw geography: replay has to rebuild the same
       -- {lat,lng} a live forward sends. '' when absent, so the column stays
       -- non-null and the scan can't fail on a message with no location.
       COALESCE(ST_AsGeoJSON(m.body_location), '')::text AS body_location,
       a.type AS account_type,
       c.workflow_url
FROM messages m
JOIN channels c ON c.id = m.channel_id
JOIN accounts a ON a.id = m.account_id
WHERE m.tenant_id = $1
  AND m.direction = 'inbound'
  AND m.workflow_fired = FALSE
  AND m.policy_action IN ('routed', 'execute')
  AND c.workflow_url IS NOT NULL
  AND c.workflow_url <> ''
  AND m.received_at >= $2
  AND (sqlc.narg('channel_id')::uuid IS NULL OR m.channel_id = sqlc.narg('channel_id')::uuid)
ORDER BY m.received_at ASC
LIMIT $3;

-- name: GetMessageForOutbound :one
-- Resolve the conversation context for a reply: the account it arrived on, the
-- original sender to reply to, and raw_payload (email replies thread off the
-- original RFC Message-ID carried there).
SELECT account_id, channel_id, tenant_id, sender_endpoint, sender_contact_id, raw_payload
FROM messages WHERE id = $1;
