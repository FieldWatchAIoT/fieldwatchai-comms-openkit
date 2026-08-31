-- Queries behind retention and erasure.
--
-- Two different obligations, deliberately kept apart:
--
--   Retention  — delete everything older than a window the tenant sets. Blunt,
--                scheduled, applies to all traffic.
--   Erasure    — remove one person's personal data on request, without
--                destroying the operational record that a message existed.
--
-- Erasure redacts rather than deletes. A disaster response's message history is
-- an operational and often legal record: a missing-person report that vanishes
-- entirely can make an after-action review impossible and, in some
-- jurisdictions, breaks a retention duty that sits alongside the erasure right.
-- Redaction removes the identifying content — who sent it, what they said, the
-- verbatim provider envelope — while leaving the fact, timing, and routing of
-- the message intact. Where a tenant genuinely needs the row gone, retention
-- purge does that.

-- name: CountMessagesOlderThan :one
SELECT count(*) FROM messages WHERE tenant_id = $1 AND received_at < $2;

-- name: PurgeMessagesOlderThan :execrows
-- Hard delete. in_reply_to_message_id is self-referential, so clear inbound
-- references first (below) or the FK blocks the delete.
DELETE FROM messages WHERE tenant_id = $1 AND received_at < $2;

-- name: ClearReplyLinksToPurgedMessages :execrows
-- Detach replies that point at messages about to be purged, so the delete does
-- not fail on messages_in_reply_to_message_id_fkey. The surviving message keeps
-- its own content; it just no longer points at a row that will not exist.
UPDATE messages m SET in_reply_to_message_id = NULL
WHERE m.tenant_id = $1
  AND m.in_reply_to_message_id IN (
    SELECT old.id FROM messages old
    WHERE old.tenant_id = $1 AND old.received_at < $2
  );

-- name: CountMessagesForEndpoint :one
SELECT count(*) FROM messages WHERE tenant_id = $1 AND sender_endpoint = $2;

-- name: RedactMessagesForEndpoint :execrows
-- Erasure keyed on the endpoint — the phone number, address or handle a person
-- actually appears under in the messages table.
--
-- This is the query that does the real work. sender_contact_id exists in the
-- schema but ingest does not populate it: resolving an inbound sender to an
-- address-book entry is not something the pipeline does yet, so the identifying
-- value on a stored message is sender_endpoint. A data-subject request also
-- arrives as an endpoint ("delete my data, my number is …"), not as an internal
-- contact id, so this matches how the request is actually made.
UPDATE messages
SET sender_endpoint  = NULL,
    body_text        = '',
    body_attachments = NULL,
    body_location    = NULL,
    raw_payload      = '{"redacted": true}'::jsonb,
    parsed           = COALESCE(parsed, '{}'::jsonb) || '{"redacted": true}'::jsonb
WHERE tenant_id = $1 AND sender_endpoint = $2;

-- name: ListEndpointsForContact :many
SELECT endpoint FROM contact_endpoints WHERE contact_id = $1;

-- name: RedactMessagesForContact :execrows
-- Erasure. Strips the personal content and the raw envelope, and marks the row
-- so a later reader can tell redaction from an empty message.
UPDATE messages
SET sender_endpoint = NULL,
    body_text       = '',
    body_attachments = NULL,
    body_location   = NULL,
    raw_payload     = '{"redacted": true}'::jsonb,
    parsed          = COALESCE(parsed, '{}'::jsonb) || '{"redacted": true}'::jsonb
WHERE tenant_id = $1
  AND (sender_contact_id = $2 OR recipient_contact_id = $2);

-- name: DeleteEndpointsForContact :execrows
DELETE FROM contact_endpoints WHERE contact_id = $1;

-- name: DetachMessagesFromContact :execrows
-- Drop the link between the message and the person, after redaction. The
-- message survives as an anonymous record of traffic.
UPDATE messages SET sender_contact_id = NULL, recipient_contact_id = NULL
WHERE tenant_id = $1 AND (sender_contact_id = $2 OR recipient_contact_id = $2);

-- name: DeleteContactRow :execrows
DELETE FROM contacts WHERE id = $1 AND tenant_id = $2;

-- name: CountMessagesForContact :one
SELECT count(*) FROM messages
WHERE tenant_id = $1 AND (sender_contact_id = $2 OR recipient_contact_id = $2);
