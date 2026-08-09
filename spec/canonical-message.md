# Canonical Message

**Version:** 1.0
**Status:** stable
**JSON Schema:** [`canonical-message.schema.json`](./canonical-message.schema.json)

The canonical message is the normalized wire format that every inbound
transport adapter emits and every downstream consumer accepts. It is what
turns "a WhatsApp text, a Twilio SMS, a Telegram photo, and an email" into
"an inbound message" — one shape that the routing layer, the parser, the
storage layer, and the AI teammate all speak.

If you are building an adapter, this document plus the JSON Schema are the
contract you must satisfy.

## Design principles

1. **Every field is always present.** A field the source platform did not
   populate is emitted as JSON `null`, never omitted, never an empty string,
   never a placeholder. Downstream code can read `msg.sender.email` without
   checking whether the key exists.
2. **Attachments and location are exceptions to the "null" rule.**
   `body.attachments` is always an array — an empty array means no
   attachments. `body.location` is either an object with `lat` and `lng` or
   `null`; there is no "empty location".
3. **`raw_payload` preserves the truth.** The adapter's parse output is a
   best-effort projection of the platform's payload. When something looks
   wrong downstream, the truth is in `raw_payload`. Adapters MUST NOT edit,
   trim, or "clean up" the raw payload.
4. **`platform_message_id` is the idempotency key.** The same message
   delivered twice by the platform (retries, at-least-once queues) must
   carry the same `platform_message_id` both times. Consumers dedupe on it.
5. **Adapters do not re-host media.** Attachment URLs point at the
   platform's own hosting. Consumers that need the bytes fetch them — with
   the platform's auth if needed — themselves. This keeps the message small
   and keeps liability for hosted media with the platform.
6. **`endpoint` is the reply-to.** Whatever a downstream `send` call would
   need as its destination goes here. For SMS / WhatsApp that is an E.164
   phone number. For email, a mailbox. For Telegram, the chat id as a
   string. Normalize before emitting.

## Structure

```json
{
  "sender": {
    "endpoint": "string",
    "platform": "string",
    "handle": "string | null",
    "full_name": "string | null",
    "first_name": "string | null",
    "last_name": "string | null",
    "email": "string | null",
    "avatar_url": "string | null"
  },
  "body": {
    "text": "string | null",
    "attachments": [
      { "type": "image|video|audio|document|sticker", "url": "string", "mime": "string | null" }
    ],
    "location": { "lat": number, "lng": number } // or null
  },
  "meta": {
    "platform_message_id": "string",
    "received_at": "RFC3339 UTC string",
    "in_reply_to_id": "string | null",
    "account_id": "string",
    "raw_payload": "any JSON value, verbatim from the platform"
  }
}
```

## Field-by-field

### `sender.endpoint` (required)

The platform-specific address. This is the value a downstream `send` call
uses as its destination.

- **whatsapp / sms** → E.164 phone number, no `whatsapp:` prefix.
  Example: `"+12421234567"`
- **telegram** → chat id as a decimal string.
  Example: `"123456789"`
- **email** → bare mailbox address, lowercased, no display name.
  Example: `"alice@example.org"`

### `sender.platform` (required)

Normalized platform identifier, lowercase, ASCII, no hyphens. Reserved
values: `whatsapp`, `sms`, `telegram`, `email`, `satellite`, `voice`. New
values SHOULD be proposed via a Spec Change Proposal issue before use, to
avoid two adapters shipping different strings for the same platform.

### `sender.handle`, `sender.full_name`, `sender.first_name`, `sender.last_name`, `sender.email`, `sender.avatar_url`

Best-effort. `null` when the platform does not report the value or the
adapter cannot obtain it without a side effect (extra API call, media
download).

### `body.text`

Plain text. For platforms that carry both `text/plain` and `text/html`
(email), adapters MUST prefer `text/plain`. `null` when the message
carries no text at all — a location share, a bare image.

### `body.attachments`

Array; empty when there are none. Never `null`. Each attachment has:

- `type` — one of `image`, `video`, `audio`, `document`, `sticker`.
- `url` — link to the platform's hosting. Adapters MUST NOT re-host.
- `mime` — MIME type as reported by the platform, or `null`.

Some platforms hold media behind their own auth (Twilio, Telegram). It is
the consumer's job to authenticate the fetch. The adapter passes the URL
through unmodified.

### `body.location`

Object with `lat` and `lng` (WGS-84 decimal degrees), or `null`.

### `meta.platform_message_id` (required)

Platform's unique message id. The idempotency key. Same value on retries.

- whatsapp-ultramsg → the UltraMSG message id
- twilio-sms / twilio-whatsapp → `MessageSid`
- telegram → `update_id` (as a decimal string)
- email-ses → `mail.messageId`

### `meta.received_at` (required)

Adapter's own clock, RFC 3339, UTC. Example: `"2026-08-09T14:32:11.482Z"`.

### `meta.in_reply_to_id`

Platform message id this is a reply to, if the platform surfaces threading.
`null` otherwise.

### `meta.account_id` (required)

The consumer's internal account identifier — which of the consumer's
inbound routes / phone numbers / mailboxes this message arrived on. The
adapter resolves this by looking up the destination against the consumer's
account directory before emitting the canonical message.

### `meta.raw_payload`

Original inbound payload, verbatim. Object, array, string, or `null`. This
field exists so bugs in the parse step can be diagnosed against the actual
source without needing to reproduce the platform delivery.

## Examples

### 1. WhatsApp text via UltraMSG

```json
{
  "sender": {
    "endpoint": "+12421234567",
    "platform": "whatsapp",
    "handle": "12421234567",
    "full_name": "Alice Carter",
    "first_name": null,
    "last_name": null,
    "email": null,
    "avatar_url": null
  },
  "body": {
    "text": "42 STATUS all clear at shelter 3",
    "attachments": [],
    "location": null
  },
  "meta": {
    "platform_message_id": "true_120363021234567890@g.us_ABCD1234",
    "received_at": "2026-08-09T14:32:11.482Z",
    "in_reply_to_id": null,
    "account_id": "acct_nema_bahamas_ops",
    "raw_payload": { "event_type": "message_received", "data": { "id": "true_120363021234567890@g.us_ABCD1234", "body": "42 STATUS all clear at shelter 3" } }
  }
}
```

### 2. SMS via Twilio

```json
{
  "sender": {
    "endpoint": "+13055551212",
    "platform": "sms",
    "handle": null,
    "full_name": null,
    "first_name": null,
    "last_name": null,
    "email": null,
    "avatar_url": null
  },
  "body": {
    "text": "42 NEEDS water 40 gallons",
    "attachments": [],
    "location": null
  },
  "meta": {
    "platform_message_id": "SMabc123def456",
    "received_at": "2026-08-09T14:32:15.001Z",
    "in_reply_to_id": null,
    "account_id": "acct_shelter_ops_south",
    "raw_payload": { "MessageSid": "SMabc123def456", "From": "+13055551212", "To": "+12421110000", "Body": "42 NEEDS water 40 gallons" }
  }
}
```

### 3. Telegram photo with caption + location

```json
{
  "sender": {
    "endpoint": "789456123",
    "platform": "telegram",
    "handle": "field_ops_marsh",
    "full_name": "Ops Marsh Harbour",
    "first_name": "Ops",
    "last_name": "Marsh Harbour",
    "email": null,
    "avatar_url": null
  },
  "body": {
    "text": "42 DAMAGE dock washed out",
    "attachments": [
      { "type": "image", "url": "AgACAgIAAxkBAAI...", "mime": "image/jpeg" }
    ],
    "location": { "lat": 26.5412, "lng": -77.0625 }
  },
  "meta": {
    "platform_message_id": "918273645",
    "received_at": "2026-08-09T14:33:02.117Z",
    "in_reply_to_id": null,
    "account_id": "acct_marsh_harbour_field",
    "raw_payload": { "update_id": 918273645, "message": { "message_id": 55, "chat": { "id": 789456123 } } }
  }
}
```

> Note: `url` for a Telegram attachment is a Telegram `file_id`, not a
> resolvable URL. The consumer resolves it via `getFile` using the same
> bot's token.

### 4. Email via AWS SES

```json
{
  "sender": {
    "endpoint": "alice@example.org",
    "platform": "email",
    "handle": null,
    "full_name": "Alice Carter",
    "first_name": null,
    "last_name": null,
    "email": "alice@example.org",
    "avatar_url": null
  },
  "body": {
    "text": "Subject: shelter 3 status\n\n42 STATUS all clear at shelter 3",
    "attachments": [],
    "location": null
  },
  "meta": {
    "platform_message_id": "0000014a-f4d4-4f00-8db8-2b57fdad8be1",
    "received_at": "2026-08-09T14:34:47.221Z",
    "in_reply_to_id": null,
    "account_id": "acct_nema_email_intake",
    "raw_payload": { "notificationType": "Received", "mail": { "messageId": "0000014a-f4d4-4f00-8db8-2b57fdad8be1" } }
  }
}
```

> Note: there is no dedicated `subject` field in this version of the
> schema. Adapters that carry a subject line prepend it to `body.text` in
> the form `Subject: <subject>\n\n<body>`.

## Conformance

An adapter is spec-conforming if:

1. Every message it emits validates against
   [`canonical-message.schema.json`](./canonical-message.schema.json).
2. `meta.platform_message_id` is stable across platform retries of the
   same underlying message.
3. `meta.raw_payload` is the platform's payload verbatim (byte-for-byte
   equivalent JSON, up to key ordering).
4. `sender.endpoint` is normalized enough to be used directly as a `send`
   destination.

A consumer is spec-conforming if:

1. It accepts any message that validates against the schema.
2. It uses `meta.platform_message_id` as the dedupe key, and treats
   repeated delivery of the same id as a no-op (idempotency).
3. It does not require any field to be present with a non-null value
   beyond those the schema marks as required and non-nullable.
