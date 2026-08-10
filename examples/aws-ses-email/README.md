# `email-ses` — Email via AWS SES

**Adapter id:** `email-ses`
**Canonical platform:** `email`
**Status:** shipped both directions.

- **Inbound listener** — [`implementations/webhook-go/internal/listeners/email/`](../../implementations/webhook-go/internal/listeners/email/) (subscribed to an SNS topic that SES publishes inbound to; verifies the shared secret in the subscription URL + the topic-ARN allowlist, then normalizes the SES receipt into a canonical message).
- **Outbound integration** — [`implementations/channels-go/internal/integrations/emailses/`](../../implementations/channels-go/internal/integrations/emailses/) (builds a MIME reply preserving `In-Reply-To`/`References` for threading and calls SES v2 `SendEmail`; authenticates via the ECS task IAM role, so there is no per-account credential to store).

Inbound email over AWS Simple Email Service. This is the adapter that
lets a disaster-management agency accept reports from anyone with an
email address — including the many field volunteers who will not
install another app.

## Identity

| Field | Value |
|---|---|
| Adapter id | `email-ses` |
| Human name | AWS SES Email |
| Platform | `email` |
| Inbound URL | `/inbound/email-ses?token=<EMAIL_SES_WEBHOOK_SECRET>` |

## The SES → SNS → webhook chain

Inbound email does not arrive directly. The delivery path is:

```
  sender's mail server
       │  (SMTP)
       ▼
    AWS SES  ─── receipt rule matches
       │
       ▼
    SNS topic  ─── HTTPS subscription POSTs to us
       │
       ▼
    /inbound/email-ses?token=…
```

You need:

1. A verified SES sending / receiving domain (e.g. `fieldwatchai.io`).
2. A catch-all receipt rule that publishes to an SNS topic.
3. An SNS **HTTPS subscription** to the adapter's URL, with the
   shared token in the URL query string.

## Verify

Two layers, both fail-closed:

- **Shared token in URL** (`?token=<EMAIL_SES_WEBHOOK_SECRET>`),
  constant-time compared. SNS cannot add custom headers, so the
  secret rides in the subscription URL (same pattern as UltraMSG).
- **`TopicArn` allowlist.** The adapter is configured with the
  expected `EMAIL_SES_TOPIC_ARN`; notifications from any other
  topic are dropped (HTTP 200, log-only) so an attacker cannot
  forge a well-formed SES notification from an SNS topic you did
  not create.

**Hardening TODO:** full SNS message-signature verification
(`SigningCertURL` + `Signature`) in addition to the URL token.
Tracked as a spec-change candidate.

## SNS message types

| Type | Handling |
|---|---|
| `SubscriptionConfirmation` | Auto-confirm: adapter GETs `SubscribeURL`, then drops (HTTP 200). |
| `Notification` | Parse (see below). |
| `UnsubscribeConfirmation` | Drop (HTTP 200). |
| Anything else | Drop (HTTP 200). |

## Payload shape

`application/json`. The SNS envelope wraps an SES `Received`
notification:

```json
{
  "Type": "Notification",
  "MessageId": "...",
  "TopicArn": "arn:aws:sns:us-west-2:123:my-inbound-topic",
  "Message": "{\"notificationType\":\"Received\", ... }",
  "Timestamp": "...",
  "SignatureVersion": "1",
  "Signature": "...",
  "SigningCertURL": "...",
  "UnsubscribeURL": "..."
}
```

`Message` is a JSON string; parse it to get the SES notification:

```json
{
  "notificationType": "Received",
  "mail": {
    "messageId": "0000014a-f4d4-4f00-8db8-2b57fdad8be1",
    "source": "alice@example.org",
    "destination": ["nema-intake@fieldwatchai.io"],
    "commonHeaders": {
      "from": ["Alice Carter <alice@example.org>"],
      "to": ["support@acme.com"],
      "subject": "shelter 3 status",
      "date": "..."
    }
  },
  "receipt": {
    "recipients": ["nema-intake@fieldwatchai.io"],
    "action": { "type": "SNS", ... }
  },
  "content": "<base64 MIME>"
}
```

## Field mapping to canonical

| Canonical field | Source |
|---|---|
| `sender.endpoint` | first mailbox parsed from `mail.commonHeaders.from`, bare, lowercased |
| `sender.platform` | literal `"email"` |
| `sender.email` | same as `sender.endpoint` |
| `sender.full_name` | display-name portion of `mail.commonHeaders.from`, if present |
| `body.text` | `"Subject: <subject>\n\n<text/plain body>"` — see below |
| `body.attachments` | v1: empty. Attachments are not extracted (see V1 scope). |
| `body.location` | `null` (email has no location) |
| `meta.platform_message_id` | `mail.messageId` |
| `meta.received_at` | adapter's own wall clock |
| `meta.raw_payload` | the SES notification JSON, verbatim (not the SNS envelope) |
| `meta.account_id` | account lookup on the first entry of `receipt.recipients` |

### Extracting the body

`content` is the raw MIME email, base64-encoded. The adapter:

1. Base64-decodes `content`.
2. Walks the MIME structure.
3. Decodes quoted-printable / base64 parts as needed.
4. Prefers `text/plain` over `text/html`.
5. Prepends the subject: `Subject: <subject>\n\n<body>`.

### The `subject` decision

There is no dedicated `subject` field on the canonical message.
Adapters that carry a subject prepend it to `body.text` in the form
`Subject: <subject>\n\n<body>` so downstream consumers see the
whole thing.

## Account lookup — `receipt.recipients`, not `To:`

**Critical:** account routing uses SES's
**`receipt.recipients`** — the envelope recipient that matched the
receipt rule — **not** the `To:` header.

Rationale: many adopters will let their customers email a public
address (`support@acme.com`) that the customer's mail system
forwards to a per-account ingest mailbox
(`acme@fieldwatchai.io`). After the forward:

- `To:` still reads `support@acme.com` (wrong for routing).
- `receipt.recipients` reads `acme@fieldwatchai.io` (right).
- `From:` still reads the original customer (correct — preserved by
  the forward).

Fall-back order: `receipt.recipients` → `mail.destination` →
`mail.commonHeaders.to`.

## Drops (return HTTP 200, empty parse)

- Non-`Notification` SNS message types.
- `notificationType != "Received"` (bounces, complaints).
- Account lookup returns `not_found`.
- Message body extraction yields empty and there are no
  attachments (not applicable in v1 since attachments are
  unextracted; may change).

## Idempotency key

`mail.messageId`. SES / SNS retry semantics apply — the same id
will be redelivered on 4xx / 5xx from the subscription URL.

## V1 scope

- **SNS-inline content only.** SNS caps notifications at ~150 KB.
  Larger emails will not fit; SES can be configured to write the
  message to S3 and put a pointer in the SNS notification instead
  ("SES → S3 → SNS-with-pointer"). S3-backed content is a
  follow-up.
- **No attachment extraction.** Adopters who need attachments
  should use the S3 backing (when it lands) and fetch attachments
  from the stored MIME.

## Notes and gotchas

- **A shared inbox address is not an account key.** The delivery
  point is the account key. Two different tenants can legitimately
  share `To: support@acme.com` if they both use different
  intermediate forwards — the SES envelope disambiguates them.
- **DKIM / SPF / DMARC** live on the outbound side and are the
  consumer's concern, not the adapter's. Adopters should think
  about identity-preservation before promising customers "reply
  to us and it will look like it came from you".
- **Retention.** Emails often carry PII by default (names,
  addresses in signature blocks). Set tenant-configurable
  retention windows and make hard-delete a first-class capability.

## See also

- [Transport adapter contract](../../spec/transport-adapter.md)
- [Canonical message](../../spec/canonical-message.md)
- [AWS SES receipt rules](https://docs.aws.amazon.com/ses/latest/dg/receiving-email-action-sns.html)
