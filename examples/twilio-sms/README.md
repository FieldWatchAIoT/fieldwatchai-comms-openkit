# `twilio-sms` / `whatsapp-twilio` — SMS + WhatsApp via Twilio

**Adapter ids:** `sms-twilio`, `whatsapp-twilio`
**Canonical platforms:** `sms`, `whatsapp`
**Status:** starter README. Reference implementation code coming **Q4 2026**.

[Twilio](https://www.twilio.com/) is one of the two adapters in this
starter set that talks to real telco routes: it delivers SMS to
regular phone numbers and can deliver WhatsApp through its
partnership channel.

One Twilio listener implementation, registered as **two channels** at
two different URLs. Twilio posts the identical form-encoded payload
for both; the `whatsapp:` scheme on `From` / `To` distinguishes them.

## Identity

| Field | Value |
|---|---|
| Adapter ids | `sms-twilio`, `whatsapp-twilio` |
| Human names | Twilio SMS, Twilio WhatsApp |
| Platforms | `sms`, `whatsapp` |
| Inbound URLs | `/inbound/sms-twilio`, `/inbound/whatsapp-twilio` |

Each adapter id is also the **account-lookup type**, so a phone
number used for both SMS and WhatsApp needs both an `sms-twilio`
account and a `whatsapp-twilio` account seeded.

## Verify

Twilio signs each webhook with **`X-Twilio-Signature`**:

```
X-Twilio-Signature = base64(HMAC-SHA1(
    key   = TWILIO_AUTH_TOKEN,
    input = publicURL + join(sorted(form_params).map(k => k + form_params[k]))
))
```

Rules:

- The signed URL is the **public URL Twilio POSTed to** — behind a
  load balancer, this is not the request's internal host. The
  adapter must rebuild the signed URL from a configured
  `PUBLIC_BASE_URL` + the request URI.
- Constant-time compare. Fail-closed → HTTP 401,
  log `invalid_signature`.
- Tested against Twilio's official signature test vector before
  the adapter goes live.

## Payload shape

`application/x-www-form-urlencoded`. A typical SMS:

```
MessageSid=SMabc123def456
From=%2B13055551212
To=%2B12421110000
Body=42+NEEDS+water+40+gallons
NumMedia=0
```

A typical WhatsApp:

```
MessageSid=SMxyz...
From=whatsapp%3A%2B13055551212
To=whatsapp%3A%2B12421110000
Body=42+STATUS+all+clear
ProfileName=Alice+Carter
WaId=13055551212
NumMedia=1
MediaUrl0=https%3A%2F%2Fapi.twilio.com%2F...
MediaContentType0=image%2Fjpeg
Latitude=26.5412
Longitude=-77.0625
```

## Field mapping to canonical

| Canonical field | Source |
|---|---|
| `sender.endpoint` | `From` — strip `whatsapp:` prefix; keep the `+` |
| `sender.platform` | `whatsapp` if `From` starts with `whatsapp:` else `sms` |
| `sender.handle` | `WaId` (WhatsApp only), else `null` |
| `sender.full_name` | `ProfileName` (WhatsApp only), else `null` |
| `body.text` | `Body` |
| `body.attachments` | for `i` in `0..NumMedia-1`: `{type: from(MediaContentType{i}), url: MediaUrl{i}, mime: MediaContentType{i}}` — coarse `type` derived from the MIME major |
| `body.location` | `{lat: Latitude, lng: Longitude}` when both present |
| `meta.platform_message_id` | `MessageSid` |
| `meta.received_at` | adapter's own wall clock |
| `meta.raw_payload` | form fields rendered as a JSON object |
| `meta.account_id` | account lookup on `To` |

## Account lookup

Key on the receiving Twilio number (`To`, with `whatsapp:` stripped).
A number that receives both SMS and WhatsApp needs accounts seeded
against **both** adapter ids:

```
lookup(type="sms-twilio", identifier="+12421110000")
lookup(type="whatsapp-twilio", identifier="+12421110000")
```

## Media

Twilio hosts media at authenticated URLs. `MediaUrl{n}` is
fetchable with HTTP Basic auth using the account SID as username
and the auth token as password. The adapter passes the URL
through; **the consumer** authenticates the fetch. Adopters MUST
provision Basic-auth-capable fetching for the consumer.

## Drops (return HTTP 200, empty parse)

- Delivery-status callbacks that arrive at the same URL by
  misconfiguration (`MessageStatus` fields present but no `Body` /
  `NumMedia`).
- Account lookup returns `not_found`.

## Idempotency key

`MessageSid`. Twilio retries on non-2xx.

## Notes and gotchas

- **`X-Twilio-Signature` is HMAC-SHA1, not SHA-256.** Yes, in 2026.
  Match Twilio's algorithm exactly.
- **URL parameters are excluded from the signature**, only form
  params. Twilio's docs on this are more subtle than you'd like;
  the safest thing is to test against Twilio's published example
  first.
- **WhatsApp session windows.** Same 24-hour rule as UltraMSG.
  Templates required for out-of-window outbound.
- **`ProfileName` can be a display-name spoof.** Do not use it for
  authorization. Use `From` (the E.164 number) as the identity.

## See also

- [Transport adapter contract](../../spec/transport-adapter.md)
- [Canonical message](../../spec/canonical-message.md)
- [Twilio: security — request validation](https://www.twilio.com/docs/usage/security)
