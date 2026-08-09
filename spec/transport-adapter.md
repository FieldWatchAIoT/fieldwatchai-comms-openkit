# Transport Adapter Contract

**Version:** 1.0
**Status:** stable
**Applies to:** any component that terminates a platform's inbound webhook /
polling loop and normalizes into the canonical message, and/or that renders a
canonical outbound message into a platform-native `send` call.

An adapter is the code that lets one messaging platform (WhatsApp via
UltraMSG, SMS via Twilio, Telegram, email via SES, a satellite messenger)
speak the OpenKit protocol.

## Adapter identity

Every adapter declares:

```
Adapter {
  id:                string   // stable, lowercase, hyphen-separated, unique
  human_name:        string   // for operator UI
  platform:          string   // one of the canonical `sender.platform` values
  transports:        [inbound?, outbound?, request-parser?]
  version:           string   // semver of this adapter's own release
}
```

Adapter `id` conventions used by the reference implementations:

| Platform integration | Adapter id |
|---|---|
| UltraMSG WhatsApp | `whatsapp-ultramsg` |
| Twilio WhatsApp | `whatsapp-twilio` |
| Twilio SMS | `sms-twilio` |
| Telegram Bot API | `telegram` |
| AWS SES email | `email-ses` |

The `id` is also the account-lookup type — the value the consumer's account
directory uses to key contact records for this adapter.

## Inbound: the four steps

An inbound adapter performs four steps, in order, and stops at the first
failure:

```
platform HTTP request
        │
        ▼
   1. Verify        —— fail: 401 Unauthorized, drop, log invalid_signature
        │
        ▼
   2. Parse         —— unparseable → return empty result, HTTP 200 (drop)
        │            (empty parse is the uniform "drop-with-200" path)
        ▼
   3. Account lookup —— unknown sender → empty parse → HTTP 200 (drop)
        │            —— transient lookup failure → HTTP 500 (retryable)
        ▼
   4. Emit canonical message to the consumer's ingest endpoint
```

### Step 1 — Verify

Each platform authenticates its webhooks differently. The adapter is
responsible for **fail-closed** verification: no verification, no accept.

| Adapter | Verification |
|---|---|
| `whatsapp-ultramsg` | Shared secret in URL query string (`?token=…`), constant-time compared. |
| `whatsapp-twilio` / `sms-twilio` | `X-Twilio-Signature` HMAC-SHA1 over the public URL + sorted POST params, base64. |
| `telegram` | `X-Telegram-Bot-Api-Secret-Token` header, constant-time compared to the value set on `setWebhook`. |
| `email-ses` | Shared token in URL (`?token=…`) **plus** SNS `TopicArn` allowlist. Hardening TODO: full SNS message-signature verification. |

Constant-time comparison is required. String-equality is not.

### Step 2 — Parse

Platform-native payload → canonical message
([`canonical-message.md`](./canonical-message.md)).

Rules:

- Every canonical field is populated as best the platform allows.
  Absent → `null` (or empty array for `attachments`). No omissions.
- `meta.raw_payload` is the platform's payload, verbatim. Do not edit
  it. Do not trim it. Do not "clean up" what looks like garbage.
- `meta.platform_message_id` is the platform's stable message id (see
  the per-adapter notes in `canonical-message.md`). Same value on
  retries.
- If the message is one the adapter should **drop** — the adapter's
  own echo, a group-chat non-mention, a non-message platform event —
  return an empty parse result. The dispatcher will respond 200 (see
  the response contract below).

### Step 3 — Account lookup

The adapter maps the platform-side destination (the `To` phone number,
the receiving mailbox, the bot id) to the consumer's internal account.
This is a lookup against the consumer's account directory:

```
lookup(type=<adapter.id>, identifier=<platform destination>)
   → account_id | not_found | transient_error
```

- `not_found` → treat as a drop. Empty parse. HTTP 200.
- `transient_error` (directory briefly down) → HTTP 500. The platform
  retries. The retry is safe because dedupe is on
  `platform_message_id`.

### Step 4 — Emit

The adapter emits the canonical message to the consumer's ingest
endpoint (or its own outbound queue that a drain worker services).

The emit carries:

- The full canonical message body.
- `Authorization: Bearer <internal-token>` (or the adapter runtime's
  equivalent auth).
- `Idempotency-Key: <meta.platform_message_id>` so the consumer can
  dedupe on retry.

## Optional capability: request-parser

Some platforms deliver an inbound message that does not by itself
identify which of the consumer's inbound routes it belongs to.
Telegram is the canonical example: a Telegram update has no bot id
inside it — the same webhook receives updates for whichever bot is
configured to post to it.

Adapters for such platforms need to read the **HTTP request** (the URL
path, the query string) in addition to the parsed body. The
request-parser capability declares that an adapter's `Parse` receives
the request as well as the body:

```
ParseRequest(r *http.Request, body []byte) → CanonicalMessage | Drop
```

The reference-implementation pattern for the Telegram adapter is
`/inbound/telegram?bot=<id>`, and the account lookup uses that `bot`
value as the identifier.

Adapters that don't need this can implement plain `Parse(body []byte)`.

## Outbound: the send call

An outbound adapter renders a canonical outbound message into the
platform's native send API.

Outbound is more platform-specific than inbound. The contract is:

```
Send(canonical_outbound) → SendResult
  SendResult {
    ok:                     bool
    platform_message_id:    string | null   // populated when the platform returns one on send
    retry_after_ms:         int | null      // populated on rate-limit responses
    error:                  string | null   // populated when ok = false
    error_class:            "permanent" | "transient" | null
  }
```

Rules:

- `error_class = "transient"` → the send may be retried by the caller
  (rate limit, brief platform outage, network blip). The caller SHOULD
  respect `retry_after_ms` if set.
- `error_class = "permanent"` → do not retry (invalid destination,
  message content rejected, unauthorized). Route to human triage.
- Adapters that support outbound media pass a URL through to the
  platform; they do not upload bytes. If the platform requires
  uploading bytes (some do), the adapter fetches from the URL and
  performs the upload inside `Send`.
- Adapters MUST NOT log the message body at INFO or higher log level.
  Message bodies contain PII by definition in a disaster context.

## Inbound HTTP response contract

The dispatcher (the HTTP handler wrapping an inbound adapter) MUST
return one of the following status codes:

| Status | Meaning | When |
|---|---|---|
| **200 OK** | Accepted, OR deliberately dropped. | Verify passed AND (parse succeeded and enqueue succeeded) OR (parse deliberately dropped). |
| **401 Unauthorized** | Verify failed. | Signature/secret/token check failed. Log an `invalid_signature` event; do not include the payload in the log. |
| **413 Payload Too Large** | Body over the configured cap. | Body > `MAX_BODY_BYTES` (reference default: 256 KB). |
| **500 Internal Server Error** | Retryable failure. | A parsed, dedupable message could not be durably accepted (queue write failed; account directory transiently unavailable). The platform retries. Retries are safe because dedupe is on `platform_message_id`. |

Deliberate drops (unparseable, unregistered, non-message, echo of our
own outbound) are **200, not 500**. Platforms retry on non-2xx; a 500
on a deliberate drop causes retry storms.

## Idempotency convention

The consumer dedupes on `meta.platform_message_id`. Adapters MUST NOT
generate synthetic ids or mutate the platform's id, even if the
platform's id looks ugly. The point of the raw id is that retries
carry the same value.

If a platform truly does not surface a stable id, the adapter SHOULD
synthesize one from a stable hash of the payload (e.g. `sha256` of the
sorted form fields) and document that in its README so the consumer
knows the id is synthetic.

## Configuration surface

Reference-implementation adapters accept configuration via environment
variables. The reference envs are:

```
# consumer ingest target
CHANNELS_URL                        e.g. https://ingest.example.org
INTERNAL_API_TOKEN                  bearer token the ingest endpoint requires
PUBLIC_BASE_URL                     public origin behind the load balancer (Twilio needs this to verify)

# per-adapter opt-in secrets (unset = adapter not registered)
WHATSAPP_ULTRAMSG_WEBHOOK_SECRET
TWILIO_AUTH_TOKEN
TELEGRAM_WEBHOOK_SECRET
EMAIL_SES_WEBHOOK_SECRET
EMAIL_SES_TOPIC_ARN                 SNS topic allowlist for the email adapter

# body cap
MAX_BODY_BYTES                      default 262144
```

Secrets MUST NOT be logged. Reference implementations use a redacting
`LogValue` on the config type.

## Observability

Adapters SHOULD emit structured log events with, at minimum:

| `event` value | When |
|---|---|
| `webhook_received` | Every inbound request, before verify. |
| `invalid_signature` | Verify failed. (This is the alarm-driving event; treat it as high signal.) |
| `dropped_no_id` | Parsed but no `platform_message_id`. |
| `enqueued` | Successfully accepted and enqueued for downstream ingest. |
| `webhook_accepted` | Terminal 200 response about to be returned. |

The specific transport layer for events is out of scope (CloudWatch
EMF, OTel, plain JSON to stdout — all fine). What matters is that
these five event names exist and mean the same thing across adapters,
so an operator's dashboard works regardless of which adapters are
installed.

## Verified V1 platforms

The reference implementations targeted for Q4 2026 in `examples/`
cover:

- **UltraMSG WhatsApp** — validated against real WhatsApp traffic.
- **Twilio WhatsApp** — X-Twilio-Signature verified against Twilio's
  official test vector.
- **Twilio SMS** — same adapter as Twilio WhatsApp, second channel.
- **Telegram Bot API** — secret-token verify, per-bot account lookup
  via `?bot=<id>` request-parser.
- **AWS SES email** — SNS-inline content, verify by URL token +
  TopicArn allowlist, account lookup on `receipt.recipients` (not
  the `To:` header — after a forward those differ).

## Roadmap

- **ZOLEO** — inbound via ZOLEO's webhook / email bridge.
- **Garmin inReach** — inbound via Garmin's Outbound API.
- **SPOT** — inbound via polled feed (SPOT has no push; the adapter
  is a poller with a bounded backfill window).
- **Iridium SBD** — inbound via direct-IP mode.
- **Voice** — separate spec, not a transport adapter in this sense;
  the voice channel is a bidirectional real-time gateway rather than
  a webhook receiver.

## Conformance

A transport adapter is spec-conforming if:

1. It fails closed on verify failure and returns 401.
2. Every canonical message it emits validates against
   [`canonical-message.schema.json`](./canonical-message.schema.json).
3. `meta.platform_message_id` is stable across platform retries.
4. Deliberate drops return HTTP 200; retryable failures return 500.
5. It emits the five standard observability events above.
6. Secrets are not logged at any level.
