# webhook-go — reference implementation

A single Go binary that receives inbound webhooks from messaging platforms,
normalizes each payload into the [canonical message
schema](../../spec/canonical-message.md), buffers them durably, and forwards
them to a downstream "channels" service that owns delivery, threading, and
account state.

```
inbound platform --> verify --> parse --> canonical --> durable buffer (SQS)
                                                             |
                                                             v
                                                 POST {CHANNELS_URL}/v1/ingest
```

Stateless (no primary database — the downstream channels service owns it).
Built to stay up under malformed inbound: every handler runs behind a
panic-recover middleware and a body-size cap, with graceful shutdown that
drains the load balancer via `/healthz`.

## What's included

| Platform | Transport | Status |
|----------|-----------|--------|
| UltraMSG WhatsApp | HTTP webhook, shared secret in `?token=` | shipped |
| Twilio WhatsApp | HTTP webhook, X-Twilio-Signature HMAC | shipped |
| Twilio SMS | HTTP webhook, X-Twilio-Signature HMAC | shipped |
| Telegram | HTTP webhook, X-Telegram-Bot-Api-Secret-Token | shipped |
| Email (AWS SES) | SES -> SNS -> HTTPS subscription | shipped |

Each listener is opt-in: it registers only when its secret is set in the
environment (see `.env.example`). Add your own by implementing the `Listener`
interface (see `internal/listeners/registry.go`).

## Quick start (Docker Compose)

Prerequisites: Docker Desktop or an equivalent Docker + Compose install.

```sh
git clone https://github.com/FieldWatchAIoT/fieldwatchai-comms-openkit.git
cd fieldwatchai-comms-openkit/implementations/webhook-go
docker compose up --build
```

That launches:

- `comms-webhook` on `localhost:8080` — the receiver itself
- `fakechannels` on the internal network — a tiny stub that logs every POST
  it receives and returns 201, standing in for a real downstream sender

Send it a synthetic UltraMSG WhatsApp payload:

```sh
curl -X POST 'http://localhost:8080/inbound/whatsapp-ultramsg?token=local-secret' \
     -H 'Content-Type: application/json' \
     --data @internal/listeners/ultramsg/testdata/text.json
```

Watch the `fakechannels` logs — you'll see it receive the canonical message the
webhook forwarded (subject prepended, `Idempotency-Key` = the platform message
id, bearer auth on the outbound POST).

The compose file leaves `SQS_QUEUE_URL` unset, so the queue falls back to an
in-memory buffer (not durable — dev only). Set `SQS_QUEUE_URL` to switch to
real SQS.

## Quick start (local Go)

Prerequisites: Go 1.24+.

```sh
cd implementations/webhook-go
cp .env.example .env
# edit .env — set INTERNAL_API_TOKEN + WHATSAPP_ULTRAMSG_WEBHOOK_SECRET at minimum
make run
```

Or with the compose stub running:

```sh
CHANNELS_URL=http://localhost:9090 \
INTERNAL_API_TOKEN=local-token \
WHATSAPP_ULTRAMSG_WEBHOOK_SECRET=local-secret \
ACCOUNTS_MAP='{"whatsapp-ultramsg":{"instance123":"acc_local"}}' \
go run ./cmd/server
```

Health check:

```sh
curl -s localhost:8080/healthz   # {"status":"ok"} once ready
```

## Architecture

```
cmd/
  server/           # composition root: reads config, wires listeners + queue + drain
  fakechannels/     # local dev stub of the downstream "channels" service

internal/
  accounts/         # resolve (platform, identifier) -> account_id (HTTP + in-memory)
  canonical/        # the canonical message schema this repo produces
  config/           # env-var config with secret redaction on log
  drain/            # worker: pull from queue, forward via publisher, ack/nack
  httpapi/          # server + middleware (recover, request-id, body cap, healthz)
  listeners/        # per-platform: verify -> parse -> canonical
    ultramsg/       # WhatsApp via UltraMSG
    twilio/         # WhatsApp + SMS via Twilio
    telegram/       # Telegram Bot API webhook
    email/          # AWS SES -> SNS -> HTTPS
  observability/    # slog + counters (log-shape contract that CloudWatch metric
                    # filters can parse)
  publisher/        # POST canonical message to {CHANNELS_URL}/v1/ingest
  queue/            # in-memory + SQS adapters behind a Queue interface
```

Key choices:

- **Queue is an interface.** The AWS SDK is confined to
  `internal/queue/sqs.go`. Swapping to another broker is one new adapter file.
- **Account resolution is delegated.** The webhook asks the downstream
  "channels" service `GET /v1/accounts/lookup?type=<platform>&identifier=<id>`
  and caches the result for 60s. A 404 drops the message; any other failure
  is transient and forces a retry (so a briefly-down channels never loses
  inbound).
- **Idempotency is the platform message id.** The outbound POST sets
  `Idempotency-Key: <platform_message_id>`, so redelivery from SQS is safe.
- **Secrets in logs are redacted.** `Config.LogValue` emits only whether
  each secret is set, never its value.
- **Every listener runs behind body-cap + panic-recover middleware.** One
  malformed inbound cannot take down the process.

## Adding a new listener

1. Create `internal/listeners/<platform>/` with `listener.go`, `verify.go`,
   `parse.go` (see `ultramsg/` as the reference).
2. Implement the `listeners.Listener` interface (`ID`, `Verify`, `Parse`).
3. In `cmd/server/main.go`, add a construction line inside the `run()`
   assembly. Make it opt-in — register only when its secret env var is set,
   so a deployment that doesn't use it never has to configure it.

The registry in `internal/listeners/registry.go` handles routing (`/inbound/<id>`),
account-not-found drops, and enqueue.

## Configuration reference

See [`.env.example`](.env.example) for the full env-var list. All secrets are
env vars — the container makes zero calls to Secrets Manager itself, so the
Terraform module can inject them via the ECS task definition's `secrets`
block. See [`deploy/terraform/aws/`](deploy/terraform/aws/) for a reference
deployment.

## Testing

```sh
make test    # unit + integration tests
make vet
```

The `tests/integration/` end-to-end test exercises the full accept -> queue
-> drain -> publish pipeline against the in-memory queue and an httptest
server standing in for channels.

## Deployment

- Container: [`deploy/Dockerfile`](deploy/Dockerfile) — static Go binary on
  distroless, non-root, ~10MB image.
- AWS Terraform module: [`deploy/terraform/aws/`](deploy/terraform/aws/) —
  minimal-but-real ECS Fargate + ALB + SQS stack.

## Specs this implements

- [canonical-message.md](../../spec/canonical-message.md)
- [transport-adapter.md](../../spec/transport-adapter.md)
- [parser-grammar.md](../../spec/parser-grammar.md)
- [ai-teammate-protocol.md](../../spec/ai-teammate-protocol.md)
