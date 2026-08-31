# Privacy and data handling

What this software collects, where it puts it, how long it keeps it, and how to
get it out or delete it.

This document describes the **reference implementations** in
[`implementations/`](../implementations/). It is written for the person
deciding whether their agency can deploy this, and for the person who has to
answer a data-subject request afterwards.

**It is not legal advice.** You are the data controller for your deployment. What
follows is what the software does; whether that satisfies your jurisdiction is
your assessment to make.

---

## The short version

- Messages arrive because someone chose to send them to a number or address you
  published. The software stores what they sent, who sent it, and what it made
  of it.
- Nothing is enriched, geolocated, profiled, or sold. There is no telemetry and
  no phone-home.
- Everything is in a Postgres database **you run**. There is no FieldWatch
  service in the path.
- You can export all of it as JSON Lines, delete it on a schedule, and erase one
  person's data on request.

---

## What is collected

Only what the messaging platform delivered. Adapters do not add to it.

| Data | Where it comes from | Why it is kept |
|---|---|---|
| Message text | The sender | The report itself |
| Sender endpoint — phone number, email address, or handle | The platform | To route a reply, and to recognise the same person's next message |
| Display name / push name | The platform profile | So an operator sees a person, not a number |
| Attachments | Referenced by **URL only** | The software never copies media into its own storage |
| Location | Only if the sender deliberately attached one | Field reports are frequently about a place |
| Timestamps | The platform and the receiver | Ordering and audit |
| Raw provider payload | The platform | So a message can be reconstructed exactly, and adapter bugs diagnosed |
| Parsed interpretation | Computed | What the system understood, and how confident it was |

**Contacts** — short id, display name, role, area, and endpoints — are entered by
you, not collected from traffic.

### What is never collected

No device identifiers, no contact-list harvesting, no read receipts, no typing
indicators, no background location, no behavioural analytics, no cookies (the
console stores only a session token and your theme choice, in the browser tab).

### The raw payload deserves a decision

`messages.raw_payload` is the verbatim provider envelope. It is the most
sensitive column, and often contains more than the canonical fields — profile
metadata, platform-internal ids, and occasionally fields the platform added
after this software was written.

It is kept because it is the only way to reconstruct a message exactly as sent,
which matters for both incident review and adapter debugging. But:

- it is **excluded from exports** unless you pass `include_raw=true`
- it is **never returned** by `GET /v1/messages` or shown in the console
- it is **overwritten** on erasure

If your assessment is that you should not hold it at all, drop the column. The
pipeline does not read it back.

---

## Where it goes

```
sender's app  ──►  platform (WhatsApp/Twilio/Telegram/SES)  ──►  your webhook
                                                                      │
                                                                      ▼
                                                          your queue (SQS or in-memory)
                                                                      │
                                                                      ▼
                                                          your Postgres database
                                                                      │
                                                                      ▼
                                                    your consumer product (workflow_url)
```

Every box after the platform is infrastructure **you** run. There is no step
that reaches FieldWatch, and no analytics endpoint.

**The platform is a processor you inherit.** Messages traverse WhatsApp, Twilio,
Telegram, or SES before they reach you, under those companies' terms. That
relationship is yours to assess — this software cannot change it, and no
open-source licence makes it go away. It is the single largest privacy
consideration in any deployment.

**Data residency** follows where you deploy. The AWS Terraform modules are
region-parameterised; a Caribbean or Pacific agency with a residency requirement
should read [`deployment.md`](./deployment.md) before applying them, and note
that SES email and KMS are AWS-region-bound.

---

## Secrets and encryption

- **Platform credentials** in `accounts.credentials_encrypted` are encrypted at
  rest with AES-256-GCM locally, or AWS KMS envelope encryption in production
  (`CREDENTIALS_ENCRYPTION`). They are decrypted just-in-time for a send and
  never written to disk or logged.
- **In transit**, every platform integration uses HTTPS.
- **At rest**, the message body is *not* separately encrypted — it relies on
  your database's encryption at rest. If you need column-level encryption for
  message bodies, that is a change you must make.
- **Inbound verification is fail-closed.** An unverifiable webhook is rejected.

### Logging

Message bodies are never logged at `INFO` or above. Boot-time config logging
never emits a secret's value: `channels` renders `database_url`,
`internal_api_token`, and `local_aes_key` as `***`, and `webhook` logs only
whether each secret is set (`internal_api_token_set: true`) rather than what it
is. Erasure logs that an erasure happened and how many rows it touched — never
the endpoint erased, since that would recreate the data in your logs.

Verify before deploying: your log aggregator, reverse proxy, and cloud provider
may capture request bodies independently of this software.

---

## Retention

There is **no automatic deletion**. Nothing expires on its own — deliberately,
because the right window is a policy decision that varies by jurisdiction and
by incident, and silently deleting a disaster response's records would be worse
than keeping them.

You set it. Check first:

```sh
curl -X POST http://localhost:9090/v1/retention/purge \
  -H 'Authorization: Bearer <token>' -H 'X-Tenant-ID: <tenant>' \
  -H 'Content-Type: application/json' \
  -d '{"older_than_days": 365}'
# => {"dry_run":true,"messages_matched":18422,"messages_deleted":0,...}
```

Then apply with `"dry_run": false`. **Omitting `dry_run` means `true`** — an
irreversible operation should not fire because a field was forgotten. The
minimum window is 7 days.

Run it from cron, a Kubernetes CronJob, or an EventBridge rule. Purge is a hard
delete: rows are gone, not redacted.

---

## Answering a data-subject request

### "What do you have about me?"

Export everything and filter to their endpoint:

```sh
curl -s 'http://localhost:9090/v1/export/messages?include_raw=true' \
  -H 'Authorization: Bearer <token>' -H 'X-Tenant-ID: <tenant>' \
  | grep '"sender_endpoint":"+1242XXXXXXX"'
```

Output is JSON Lines — one complete object per line, readable by any data tool,
no proprietary format.

### "Delete my data"

```sh
curl -X POST http://localhost:9090/v1/retention/erase-endpoint \
  -H 'Authorization: Bearer <token>' -H 'X-Tenant-ID: <tenant>' \
  -H 'Content-Type: application/json' \
  -d '{"endpoint": "+1242XXXXXXX", "dry_run": false}'
```

**Erasure redacts rather than deletes, and you should understand why before
relying on it.** It clears the sender endpoint, the message text, attachments,
location, and the raw envelope, and marks the row `redacted`. The row itself
survives, carrying only that a message arrived at a time on a channel.

The reasoning: a disaster response's message history is an operational and often
legal record. A missing-person report that vanishes entirely can make an
after-action review impossible and, in several jurisdictions, breaks a
record-keeping duty that sits *alongside* the erasure right rather than being
overridden by it. Redaction removes the personal data while preserving the
audit trail.

If your assessment is that the row must also go, use purge, or delete directly:

```sql
DELETE FROM messages WHERE tenant_id = $1 AND sender_endpoint = $2;
```

Erasing a **contact** (`POST /v1/contacts/{id}/erase`) additionally removes the
address-book entry and its endpoints, and redacts messages from those endpoints.

### A known limitation, stated plainly

Erasure matches on `sender_endpoint`. The `messages.sender_contact_id` column
exists but **ingest does not populate it** — resolving an inbound sender to an
address-book entry is not something the pipeline does yet. So:

- Erasing by endpoint works, and is what a subject request maps onto.
- Erasing a contact only reaches messages from endpoints **registered to that
  contact**. If someone messaged you from a second number you never recorded,
  that traffic is not reached.

Confirm coverage with a dry run, which reports `messages_matched` before
anything changes.

---

## Multi-tenancy

Every domain table carries `tenant_id`, and every handler scopes its queries to
the `X-Tenant-ID` header. There is no cross-tenant read path in the service.

Two honest caveats:

- **`INTERNAL_API_TOKEN` is a shared admin credential**, not a per-user
  identity. Anyone holding it can act as any tenant. Per-actor JWTs are a
  future revision. Treat that token as a root password, and do not hand it to
  people you would not give database access.
- **Account lookup is deliberately cross-tenant** (`GET /v1/accounts/lookup`),
  because the webhook must resolve an inbound message before it knows the
  tenant. It returns only ids — never credentials or message content.

---

## Children and vulnerable people

Disaster traffic will include messages from and about children, injured people,
and people in acute distress. The software cannot detect this and does not try.

Two consequences worth planning for:

- **Do not treat a message body as safe to display publicly.** Nothing here
  sanitises or classifies content.
- Your retention window applies uniformly. If you need shorter retention for a
  subset — a missing-child report, say — you must implement that separately.

See [`harms-analysis.md`](./harms-analysis.md) for the wider misuse assessment.

---

## Reporting a privacy problem

Please do not open a public issue. See [`SECURITY.md`](../SECURITY.md), or email
`security@fieldwatchai.io`.

If you find that something in this document is not true of the code, that is a
bug — report it the same way, and we will fix the code or the document.
