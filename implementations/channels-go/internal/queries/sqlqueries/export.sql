-- Queries behind the data-export endpoints.
--
-- Export exists because an adopter must be able to leave. A tool that holds a
-- disaster response's message history in a database only its own code can read
-- is not something a public agency should adopt, whatever its licence says.
-- These stream the whole dataset in stable, non-proprietary JSON — no caps, no
-- sampling, and keyset pagination so an export of any size runs in constant
-- memory rather than loading the table.

-- name: ExportMessagesPage :many
-- One page, oldest first, using a keyset cursor rather than OFFSET: OFFSET
-- re-scans on every page and, worse, silently skips or repeats rows when
-- traffic arrives mid-export. (received_at, id) is a total order because id is
-- unique, so the cursor never straddles a tie.
SELECT id, tenant_id, account_id, channel_id, direction,
       sender_contact_id, recipient_contact_id, sender_endpoint,
       body_text, body_attachments,
       -- COALESCE, because ST_AsGeoJSON returns NULL for a message with no
       -- location — which is most of them — and a NULL here fails the scan.
       COALESCE(ST_AsGeoJSON(body_location), '')::text AS body_location_geojson,
       parsed, policy_action, workflow_fired, in_reply_to_message_id,
       platform_message_id, raw_payload, received_at, processed_at
FROM messages
WHERE tenant_id = $1
  AND (received_at, id) > (sqlc.arg('after_received_at')::timestamptz, sqlc.arg('after_id')::uuid)
ORDER BY received_at, id
LIMIT $2;

-- name: ExportContactsPage :many
-- The address book, with each contact's endpoints inlined by the caller. Same
-- keyset approach, ordered by the pair that is unique per tenant.
SELECT id, tenant_id, short_id, display_name, aoi_id, role,
       default_channel_id, status, metadata, created_at, updated_at
FROM contacts
WHERE tenant_id = $1
  AND (created_at, id) > (sqlc.arg('after_created_at')::timestamptz, sqlc.arg('after_id')::uuid)
ORDER BY created_at, id
LIMIT $2;

-- name: ExportEndpointsForContacts :many
-- Endpoints for a page of contacts, fetched in one round trip rather than per
-- contact.
SELECT ce.id, ce.contact_id, ce.channel_id, ce.account_id, ce.endpoint,
       ce.priority, ce.capabilities, ce.last_seen_at
FROM contact_endpoints ce
WHERE ce.contact_id = ANY(sqlc.arg('contact_ids')::uuid[])
ORDER BY ce.contact_id, ce.priority DESC;
