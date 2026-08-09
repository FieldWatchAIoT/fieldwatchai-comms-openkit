# Examples — reference transport adapter starters

This folder contains starter material for building **transport adapters**
that implement the [transport adapter contract](../spec/transport-adapter.md)
and emit valid [canonical messages](../spec/canonical-message.md).

Each subfolder covers one platform integration: what the platform sends,
how to verify it, how to normalize the payload, and how to map the
platform's destination to a consumer account.

## What is here now

| Adapter | Platform | Code | README |
|---|---|---|---|
| `whatsapp-ultramsg` | WhatsApp via [UltraMSG](https://ultramsg.com/) | coming Q4 2026 | [read](./whatsapp-ultramsg/README.md) |
| `twilio-sms` | SMS + WhatsApp via [Twilio](https://www.twilio.com/) | coming Q4 2026 | [read](./twilio-sms/README.md) |
| `telegram-bot` | Telegram via [Bot API](https://core.telegram.org/bots/api) | coming Q4 2026 | [read](./telegram-bot/README.md) |
| `aws-ses-email` | Email via [AWS SES](https://aws.amazon.com/ses/) inbound | coming Q4 2026 | [read](./aws-ses-email/README.md) |

Each starter README today explains the shape of the adapter — the
verify pattern, the payload shape, the account lookup — so an
implementer can start writing against the transport-adapter contract
before the reference code lands. When the reference code lands
(target: Q4 2026), it will be added to the same folders alongside
the READMEs.

## Coming on the roadmap

| Adapter | Platform | Target |
|---|---|---|
| `zoleo` | ZOLEO satellite messenger | Q1 2027 |
| `garmin-inreach` | Garmin inReach (Outbound API) | Q1 2027 |
| `spot` | SPOT satellite messenger (polled) | Q1 2027 |
| `iridium-sbd` | Iridium Short Burst Data (direct-IP) | Q2 2027 |
| `voice-vapi` | Voice channel via Vapi / ElevenLabs pattern | Q2 2027 (separate spec, not a webhook adapter) |

## Guidance for a contributed adapter

If you would like to contribute a new transport adapter, please:

1. Open a feature-request issue first, briefly describing the
   platform and how you plan to verify inbound.
2. Read the [transport adapter contract](../spec/transport-adapter.md)
   in full — the response-code contract (200 for deliberate drops,
   500 for retryable failures, 401 for verify failure) is easy to
   get wrong and is load-bearing.
3. Publish your adapter as a folder next to the ones already here,
   with a README following roughly the same shape (identity,
   verify, parse, account lookup, notes).

We can accept adapter contributions in any implementation language
that has a case for the target platform — Go is preferred because
the rest of the reference stack is Go, but if a platform's SDK is
much better in Python or Node we would rather have a good Python /
Node adapter than a mediocre Go one.

## What a starter README should cover

Adapter starter READMEs in this folder answer, for the platform they
integrate with:

1. **Identity** — adapter id, platform id, human name.
2. **Verify** — how inbound is authenticated, what header / query
   parameter carries the secret, whether it is HMAC or shared-secret.
3. **Payload shape** — what the platform sends and where in the
   payload each canonical-message field comes from.
4. **Account lookup** — how the platform's `To` / destination maps
   to a consumer account.
5. **Media** — whether attachments are URLs, file ids, or bytes;
   whether the URL requires platform auth to fetch.
6. **Idempotency** — which platform field serves as
   `meta.platform_message_id`.
7. **Notes and gotchas** — anything an implementer would otherwise
   discover the hard way.
