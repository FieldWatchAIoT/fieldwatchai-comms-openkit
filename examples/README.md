# Examples — reference transport adapter starters

This folder contains starter material for building **transport adapters**
that implement the [transport adapter contract](../spec/transport-adapter.md)
and emit valid [canonical messages](../spec/canonical-message.md).

Each subfolder covers one platform integration: what the platform sends,
how to verify it, how to normalize the payload, and how to map the
platform's destination to a consumer account.

## What is here now

Every adapter listed below now ships as a working listener inside the
[Go reference implementation](../implementations/webhook-go/). These
starter READMEs remain as the platform-level explanation (identity,
verify, payload shape, gotchas) and point at the working code in
`implementations/webhook-go/internal/listeners/<platform>/`.

| Adapter | Platform | Working code | Starter README |
|---|---|---|---|
| `whatsapp-ultramsg` | WhatsApp via [UltraMSG](https://ultramsg.com/) | [`internal/listeners/ultramsg/`](../implementations/webhook-go/internal/listeners/ultramsg/) | [read](./whatsapp-ultramsg/README.md) |
| `twilio-sms` | SMS + WhatsApp via [Twilio](https://www.twilio.com/) | [`internal/listeners/twilio/`](../implementations/webhook-go/internal/listeners/twilio/) | [read](./twilio-sms/README.md) |
| `telegram-bot` | Telegram via [Bot API](https://core.telegram.org/bots/api) | [`internal/listeners/telegram/`](../implementations/webhook-go/internal/listeners/telegram/) | [read](./telegram-bot/README.md) |
| `aws-ses-email` | Email via [AWS SES](https://aws.amazon.com/ses/) inbound | [`internal/listeners/email/`](../implementations/webhook-go/internal/listeners/email/) | [read](./aws-ses-email/README.md) |

To try any of them:

```sh
cd implementations/webhook-go
docker compose up --build
# then curl the /inbound/<adapter-id> endpoint (see impl README for a walkthrough)
```

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
