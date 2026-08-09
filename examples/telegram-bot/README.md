# `telegram` — Telegram via Bot API

**Adapter id:** `telegram`
**Canonical platform:** `telegram`
**Status:** starter README. Reference implementation code coming **Q4 2026**.

[Telegram Bot API](https://core.telegram.org/bots/api) delivers
inbound messages to a webhook you register via `setWebhook`. Each
bot has its own token; each token authenticates both inbound
(sender-verification via a shared secret) and outbound.

## Identity

| Field | Value |
|---|---|
| Adapter id | `telegram` |
| Human name | Telegram |
| Platform | `telegram` |
| Inbound URL | `/inbound/telegram?bot=<bot-id>` |

## Verify

Telegram does not HMAC-sign updates. It supports a
**shared secret header** that it echoes on every webhook delivery:

```
X-Telegram-Bot-Api-Secret-Token: <TELEGRAM_WEBHOOK_SECRET>
```

The secret is set on `setWebhook` (parameter `secret_token`) and
constant-time compared on every inbound. Fail-closed → HTTP 401,
log `invalid_signature`.

## The `?bot=<id>` request-parser pattern

Telegram updates carry no bot id inside the payload — the same
webhook URL receives updates for whichever bot is configured to post
to it. The adapter reads the bot id from the **URL query string**
(`?bot=<id>`) via the [request-parser capability](../../spec/transport-adapter.md#optional-capability-request-parser)
declared in the transport adapter contract.

The `bot` value is then used as the account-lookup identifier:

```
lookup(type="telegram", identifier="<bot-id>")
```

Each bot = one account. This is the "UltraMSG-like" model rather
than the "one Twilio auth token for many numbers" model.

## Payload shape

`application/json`. A typical inbound text message
([`Update`](https://core.telegram.org/bots/api#update) →
[`Message`](https://core.telegram.org/bots/api#message)):

```json
{
  "update_id": 918273645,
  "message": {
    "message_id": 55,
    "from": {
      "id": 789456123,
      "is_bot": false,
      "first_name": "Ops",
      "last_name": "Marsh Harbour",
      "username": "field_ops_marsh",
      "language_code": "en"
    },
    "chat": {
      "id": 789456123,
      "type": "private"
    },
    "date": 1755000000,
    "text": "42 STATUS shelter 3 all clear"
  }
}
```

## Field mapping to canonical

| Canonical field | Source |
|---|---|
| `sender.endpoint` | `message.chat.id` as a decimal string (this is the reply target — Telegram has no phone number) |
| `sender.platform` | literal `"telegram"` |
| `sender.handle` | `message.from.username` |
| `sender.full_name` | `message.from.first_name` + " " + `message.from.last_name` (whichever exist) |
| `sender.first_name` | `message.from.first_name` |
| `sender.last_name` | `message.from.last_name` |
| `body.text` | `message.text` OR `message.caption` (for photo / doc messages) |
| `body.attachments` | derived from `message.photo` / `document` / `video` / `voice` / `audio` / `sticker` |
| `body.location` | `message.location` when present |
| `meta.platform_message_id` | `update_id` as a decimal string |
| `meta.received_at` | adapter's own wall clock |
| `meta.in_reply_to_id` | `message.reply_to_message.message_id` if present |
| `meta.raw_payload` | full request body, verbatim |
| `meta.account_id` | account lookup on the `?bot=<id>` query value |

## Media

Telegram attachments are `file_id` values, not fetchable URLs. The
adapter passes the `file_id` as the attachment `url`. The consumer
resolves the `file_id` via
[`getFile`](https://core.telegram.org/bots/api#getfile) using the
per-bot token, which the consumer already needs for outbound. This
mirrors the Twilio "media auth is the channels-side's concern"
pattern.

## Drops (return HTTP 200, empty parse)

- Non-`message` updates (`edited_message`, `channel_post`,
  `callback_query`, etc. — v1 handles `message` only).
- `message.from.is_bot == true` (another bot posting; usually noise).
- Missing `?bot=` query parameter.
- Account lookup returns `not_found`.

## Idempotency key

`update_id`, as a decimal string. Telegram re-delivers updates on
non-2xx responses; the same `update_id` will be sent both times.

## Notes and gotchas

- **Bot token lives in the consumer**, not the adapter. The adapter
  only needs the shared webhook secret to verify inbound. Outbound
  (send + `getFile`) uses the bot token, and that stays wherever
  outbound / media-fetch happens.
- **Chat id is the reply target.** For a private chat this is the
  user's id. For a group chat this is the group id (negative
  integer). For channels it is different again. Adopters should
  decide policy on group and channel messages before going live.
- **Rate limits.** Telegram tolerates high inbound throughput to
  the webhook but caps outbound at ~30 messages/second across the
  whole bot. Design outbound with that in mind.

## See also

- [Transport adapter contract](../../spec/transport-adapter.md)
- [Canonical message](../../spec/canonical-message.md)
- [Telegram Bot API docs](https://core.telegram.org/bots/api)
