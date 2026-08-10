# `whatsapp-ultramsg` — WhatsApp via UltraMSG

**Adapter id:** `whatsapp-ultramsg`
**Canonical platform:** `whatsapp`
**Status:** shipped both directions.

- **Inbound listener** — [`implementations/webhook-go/internal/listeners/ultramsg/`](../../implementations/webhook-go/internal/listeners/ultramsg/) (verifies the shared secret, parses UltraMSG's JSON, emits a canonical message). Captured test payloads at [`testdata/`](../../implementations/webhook-go/internal/listeners/ultramsg/testdata/).
- **Outbound integration** — [`implementations/channels-go/internal/integrations/ultramsg/`](../../implementations/channels-go/internal/integrations/ultramsg/) (decrypts the account's UltraMSG credentials via the KMS/AES Encryptor and POSTs the reply to UltraMSG's `messages/chat` endpoint).

Round-trip it end-to-end locally with the root docker-compose:

```sh
cd ../../          # to the repo root
docker compose up --build
# in another terminal:
curl -X POST 'http://localhost:8080/inbound/whatsapp-ultramsg?token=local-secret' \
     -H 'Content-Type: application/json' \
     --data @implementations/webhook-go/internal/listeners/ultramsg/testdata/text.json
```

The rest of this README explains the platform-level shape of the adapter.

[UltraMSG](https://ultramsg.com/) is a WhatsApp Business gateway that
delivers inbound messages to a configured webhook and accepts outbound
messages via a REST API. It is the lowest-cost path to a working
WhatsApp channel for a small NGO or a first-deployment agency.

## Identity

| Field | Value |
|---|---|
| Adapter id | `whatsapp-ultramsg` |
| Human name | UltraMSG WhatsApp |
| Platform | `whatsapp` |

## Verify

UltraMSG does not sign requests and cannot add custom headers. The
recommended pattern is a **shared secret in the URL query string**:

```
POST /inbound/whatsapp-ultramsg?token=<WHATSAPP_ULTRAMSG_WEBHOOK_SECRET>
```

The adapter reads `token` from the query string and constant-time
compares to the configured `WHATSAPP_ULTRAMSG_WEBHOOK_SECRET`.
Missing or mismatched token → HTTP 401, log `invalid_signature`.

Rotate the token via UltraMSG's webhook config any time; the adapter
supports a hot secret change on next request.

## Payload shape

UltraMSG posts `application/json`. A typical inbound text message:

```json
{
  "event_type": "message_received",
  "instance_id": "instance12345",
  "data": {
    "id": "true_120363021234567890@g.us_ABCD1234",
    "from": "12421234567@c.us",
    "to": "12421110000@c.us",
    "author": "",
    "pushname": "Alice Carter",
    "ack": "",
    "type": "chat",
    "body": "42 STATUS all clear at shelter 3",
    "media": "",
    "fromMe": false,
    "self": false,
    "isForwarded": false,
    "isMentioned": false,
    "quotedMsg": {},
    "mentionedIds": [],
    "time": 1755000000
  }
}
```

## Field mapping to canonical

| Canonical field | Source |
|---|---|
| `sender.endpoint` | `data.from` → strip `@c.us`, prepend `+` → E.164 |
| `sender.platform` | literal `"whatsapp"` |
| `sender.handle` | `data.from` → strip `@c.us` |
| `sender.full_name` | `data.pushname` |
| `body.text` | `data.body` |
| `body.attachments` | derived from `data.type` + `data.media` (populated when `type` in {`image`, `video`, `audio`, `document`, `ptt`, `sticker`}) |
| `body.location` | derived from `data.location.lat` / `data.location.lng` when `type == "location"` |
| `meta.platform_message_id` | `data.id` |
| `meta.received_at` | adapter's own wall clock, RFC 3339 UTC |
| `meta.raw_payload` | the full request body, verbatim |
| `meta.account_id` | account lookup by `data.to` (stripped of `@c.us`, prepended `+`) |

## Account lookup

Key on the receiving WhatsApp number (`data.to`). The consumer's
account directory is called with:

```
lookup(type="whatsapp-ultramsg", identifier="+12421110000")
   → account_id | not_found | transient_error
```

## Media

UltraMSG delivers media as a URL in `data.media`. The URL is public
UltraMSG hosting; no auth is required to fetch. The adapter passes
the URL through in the attachment; the consumer fetches when needed.

## Drops (return HTTP 200, empty parse)

- `data.fromMe == true` (echo of our own outbound).
- `event_type != "message_received"` (delivery receipts, presence
  changes).
- Account lookup returns `not_found`.
- Group-chat message where the bot was not mentioned (optional
  behavior; deployment policy).

## Idempotency key

`data.id`. UltraMSG re-delivers on non-2xx; the same `data.id` will
be sent both times.

## Notes and gotchas

- **Location parser branch is under-tested.** The docs describe a
  `data.location` object on `type == "location"` messages, but the
  reference implementation has not been fully validated against a
  captured real-world location webhook. Please open an issue if you
  have a real captured payload we can add to the test fixtures.
- **UltraMSG instance != account.** One UltraMSG account can have
  several "instances" (independent WhatsApp numbers). Each instance
  gets its own webhook URL with its own token. Do not share
  secrets across instances.
- **Business messaging window.** WhatsApp restricts outbound to
  24 hours after the last inbound from a given contact unless you
  are using an approved template. This is a channels-layer concern,
  not an adapter concern, but adopters need to know.

## See also

- [Transport adapter contract](../../spec/transport-adapter.md)
- [Canonical message](../../spec/canonical-message.md)
- [UltraMSG documentation](https://docs.ultramsg.com/)
