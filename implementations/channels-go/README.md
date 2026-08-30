# channels-go — reference implementation

The routing brain of the comms hub. This service owns the database. It accepts
[canonical messages](../../spec/canonical-message.md) from the webhook,
resolves the sender to a contact, parses the body with the rule-based
[parser grammar](../../spec/parser-grammar.md), scores confidence, runs the
policy gate (execute / echo back / clarify / drop), dispatches outbound
replies through platform integrations, and fires workflow webhooks to
downstream consumer products.

```
comms-webhook  --POST /v1/ingest-->  THIS SERVICE  ------> workflow webhook -> consumer
                                       |    ^
                                       |    |
                                       v    +---- outbound reply (WhatsApp / SMS /
                                     Postgres          Telegram / SES email)
                                    (PostGIS)
```

Stateful (owns Postgres 16 + PostGIS). Every account credential is encrypted
at rest via a pluggable `Encryptor` — AES-256-GCM locally, KMS envelope
encryption in AWS. Migrations are embedded via `//go:embed` and applied on
boot with `golang-migrate`.

## What's included

| Concern | Package | Status |
|---|---|---|
| Canonical message contract | `internal/canonical/` | shipped |
| Address-book / contact CRUD + bulk-import | `internal/contacts/` | shipped |
| Platform account CRUD (encrypted credentials) | `internal/accounts/` | shipped |
| Channel + workflow CRUD | `internal/channels/`, `internal/workflow/` | shipped |
| Rule-based command parser | `internal/parser/` | shipped |
| Fuzzy short-id resolver | `internal/resolver/`, `internal/strdist/` | shipped |
| Confidence + policy gate | `internal/policy/` | shipped |
| Ingest pipeline (parse -> resolve -> policy -> persist -> dispatch -> forward) | `internal/ingest/` | shipped |
| Operator-triggered outbound | `internal/outboundapi/`, `internal/outbound/` | shipped |
| Unfired-workflow replay | `internal/replay/` | shipped |
| WhatsApp — UltraMSG | `internal/integrations/ultramsg/` | shipped |
| WhatsApp + SMS — Twilio | `internal/integrations/twilio/` | shipped |
| Telegram | `internal/integrations/telegram/` | shipped |
| Email — AWS SES v2 | `internal/integrations/emailses/` | shipped |
| Credential encryption (AES-GCM / KMS) | `internal/crypto/` | shipped |

## Quick start (Docker Compose)

Prerequisites: Docker Desktop or equivalent Docker + Compose.

```sh
git clone https://github.com/FieldWatchAIoT/fieldwatchai-comms-openkit.git
cd fieldwatchai-comms-openkit/implementations/channels-go
docker compose up --build
```

That launches:

- `db` — Postgres 16 with PostGIS on `localhost:5434`
- `comms-channels` on `localhost:9090` — the service itself

Migrations apply on boot (see `internal/db/migrate.go`). Confirm it's up:

```sh
curl -s localhost:9090/healthz         # {"status":"ok"}
```

Look at the schema:

```sh
docker compose exec db psql -U openkit -d openkit -c '\dt'
```

> Looking for the full end-to-end demo (webhook -> channels -> DB)? Use the
> `docker-compose.yml` at the **repo root** instead of this one — it brings
> up both implementations plus LocalStack together.

## Quick start (local Go)

Prerequisites: Go 1.24+, Postgres 16 with PostGIS.

```sh
docker compose up -d db                # or your own Postgres+PostGIS
cp .env.example .env
export $(grep -v '^#' .env | xargs)
make run
```

Sandboxed shell without a module proxy? Use:

```sh
make test GO="GOTOOLCHAIN=local GOPROXY=off GOFLAGS=-mod=mod go"
```

## Database

Postgres 16 + PostGIS. This service owns the schema:

- `tenants` — top-level tenancy
- `accounts` — a platform-side inbox / bot / phone number (encrypted creds)
- `channels` — a named routing target with its own parser config + workflow URL
- `channel_accounts` — many-to-many, with a direction + priority for outbound picking
- `contacts` + `contact_endpoints` — the address book keyed by short_id
- `groups` + `group_members` — broadcast targets (aoi / role / tag / static)
- `messages` — every inbound + outbound message, with `body_location` GEOGRAPHY
- `workflows` — per-channel, per-command action definitions
- `audit_log` — append-only operator + system actions

See [`migrations/000001_init.up.sql`](migrations/000001_init.up.sql) for the
full DDL.

### Migrations

Migrations live in `migrations/` as pairs of `NNNNNN_name.up.sql` and
`.down.sql`. The `//go:embed` directive in `migrations/embed.go` pulls the
`.sql` files into the binary; on boot, `internal/db/migrate.go` opens an
`iofs` source and runs golang-migrate with an advisory lock. So a
freshly-deployed container is always at the head revision by the time it
starts serving traffic. Run migrations by hand too:

```sh
make migrate-up DATABASE_URL='postgres://openkit:openkit@localhost:5434/openkit?sslmode=disable'
```

PostGIS itself is a privileged extension — the app's migration role
intentionally cannot `CREATE EXTENSION postgis`. The `postgis/postgis:16-3.4`
Docker image and any RDS Postgres cluster you provision already have it
installed.

### Type-safe queries via sqlc

`internal/queries/sqlqueries/*.sql` are the hand-written queries; `sqlc`
regenerates `internal/queries/goqueries/*.sql.go` from them + the schema
(`migrations/*.up.sql`). Regenerate with `make sqlc` after editing a query.
Config lives in [`sqlc.yaml`](sqlc.yaml).

## Ingest pipeline

`POST /v1/ingest` accepts one canonical message. The service:

1. Resolves `platform_identifier` to the owning `account`. Unknown -> 404
   (webhook drops).
2. Resolves the sender endpoint to a `contact_endpoint` (creates a stub
   contact if none exists).
3. Runs the [parser](../../spec/parser-grammar.md) with that channel's
   `parser_config` and the tenant's short-id list.
4. Runs the resolver — fuzzy short-id matching with a confidence score and
   alternatives.
5. Applies the policy gate: `execute` / `echo_back` / `clarify` / `recalled`
   / `dropped`. Thresholds are per-channel.
6. Persists the inbound `message` row with the parsed doc + policy_action.
7. If the policy action requires a reply, dispatches it via the outbound
   registry (echo back through the same platform).
8. If the channel has a `workflow_url` and the policy action was `executed`,
   fires the forward webhook to the consumer product (best-effort in-process;
   see replay below).

## Echo back + recall

When confidence is medium, the service echoes back a paraphrase of what it
understood so the sender can confirm or correct. If the sender replies with
a correction within `recall_window_seconds`, the workflow is re-fired with the
corrected target — no double-execution. Recall is time-boxed per channel.

## Credential encryption

Every row in `accounts` stores platform API keys in `credentials_encrypted`
(BYTEA). Encryption is selected at boot via `CREDENTIALS_ENCRYPTION`:

- `localaes` — AES-256-GCM with a static key from `LOCAL_AES_KEY`. Dev / CI
  only.
- `kms` — AWS KMS envelope encryption. The container calls
  `kms:GenerateDataKey` and `kms:Decrypt` against the CMK named in
  `CREDENTIALS_KMS_KEY_ID`.

See `internal/crypto/factory.go` for the selector and `internal/crypto/kms.go`
+ `localaes.go` for the two implementations. The `Encryptor` interface is one
method each way, so a third backend (Vault, GCP KMS, etc.) is a single new
file.

## Outbound dispatch

`internal/outbound/outbound.go` is a small registry mapping a transport type
(`whatsapp`, `whatsapp-twilio`, `sms-twilio`, `telegram`, `email-ses`) to an
integration package under `internal/integrations/`. The dispatcher picks the
right integration by looking at the target `channel_account`'s `type` +
`priority`. Each integration decrypts the account's credentials just-in-time
via the `Encryptor` — decrypted material never touches disk and never lands
in a log.

Email is the outlier: SES v2 authenticates via the ECS task IAM role, so
there's no per-account credential to store — the account row's identifier is
the sending address.

## Workflow forwarding + replay

When the policy gate executes, `internal/workflow/workflow.go` POSTs the
canonical + parsed doc to the channel's `workflow_url`. Delivery is
best-effort in-process — a consumer that is briefly down misses the forward.
`POST /v1/workflows/replay` is the operator-triggered recovery: it lists
unfired forwards (from stored messages where `workflow_fired = false`) and
re-POSTs them, shape-identical to the original. Location is carried through
on the replay — a "show on map" consumer action must not silently degrade.

The forward payload shape is documented in
[../../spec/transport-adapter.md](../../spec/transport-adapter.md).

## HTTP surface

All routes are authenticated with a shared bearer token
(`INTERNAL_API_TOKEN`). JWT-per-actor lands in a future revision.

| Route | Purpose |
|---|---|
| `GET /healthz` | liveness + readiness (the only unauthenticated route) |
| `POST /v1/ingest` | accept one canonical message (called by webhook) |
| `GET /v1/accounts/lookup?type=&identifier=` | webhook uses this to resolve platform -> account_id |
| `GET /v1/accounts` | list accounts for the tenant, with routing |
| `POST /v1/accounts` | create an account |
| `PATCH /v1/accounts/{id}` · `DELETE /v1/accounts/{id}` | update / delete an account |
| `GET /v1/contacts` · `POST /v1/contacts` | list / create contacts |
| `PATCH /v1/contacts/{id}` · `DELETE /v1/contacts/{id}` | update / delete a contact |
| `POST /v1/contacts/bulk-import` | bulk address-book import |
| `GET /v1/contacts/short_id_check?short_id=` | check a short-id for collisions, with suggestions |
| `POST /v1/outbound` | operator-initiated outbound send |
| `GET /v1/channels` · `GET /v1/channels/{id}` | list / read channels (**read-only**) |
| `GET /v1/channels/{id}/accounts` | list the accounts linked to a channel |
| `POST /v1/channels/{id}/accounts` · `DELETE /v1/channels/{id}/accounts/{account_id}` | link / unlink an account |
| `POST /v1/workflows/replay` | re-fire unfired forwards for a channel |

Two gaps worth knowing before you plan around this surface:

- **There is no channel-create route.** Channels can be listed, read, and had
  accounts linked to them, but the row itself — including `workflow_url`,
  `parser_config`, and `confidence_thresholds` — is created in SQL. So
  configuring workflow forwarding today means a direct `INSERT`.
- **There is no tenant route at all.** `tenants` is provisioned out of band, and
  `accounts.tenant_id` is a foreign key onto it, so `POST /v1/accounts` against
  an unknown tenant fails the constraint and returns a bare `internal_error`.
  The root [`deploy/seed/`](../../deploy/seed/) script shows the SQL.

Requests carry `X-Tenant-Id`; the middleware surfaces it as a context value
that every handler + service uses to scope its queries. There is no
cross-tenant read path in this service.

## Configuration reference

See [`.env.example`](.env.example) for the full env-var list with defaults.
Every secret is an env var; the container makes zero Secrets Manager calls at
runtime. In AWS, the ECS task definition's `secrets` block resolves Secrets
Manager ARNs to env vars at container start — see
[`deploy/terraform/aws/`](deploy/terraform/aws/).

## Testing

```sh
make test              # unit tests (no external deps)
make vet

docker compose up -d db   # integration tests need a live Postgres+PostGIS
make test-integration
```

The integration tests under `tests/integration/` exercise the ingest pipeline
+ CRUD paths against a live Postgres, and `internal/db/` covers the pool and
migrations. All of them are behind the `integration` build tag.

> **This suite is destructive.** `TestMigrationsDownIsClean` runs
> `migrate down`, which drops every table in the target database. Never point
> it at anything whose contents you want to keep.

Because of that, compose creates a second database, `openkit_test`, on first
boot ([`deploy/postgres-init/`](../../deploy/postgres-init/)), and
`make test-integration` targets it rather than the `openkit` database the demo
uses. The target refuses outright to run against `openkit`.

The tests read **`FCC_TEST_DATABASE_URL`**, not `DATABASE_URL`. This matters
more than it looks: they `t.Skip()` when that variable is unset, and a skipped
Go test still prints `ok` — so pointing them at the wrong variable produces a
run that reports success while executing nothing. `make test-integration`
therefore sets the variable itself, runs verbosely so skips are visible, and
refuses to start when the `golang-migrate` CLI is missing (the schema tests
would otherwise skip silently too). Override it for a throwaway database of
your own:

```sh
make test-integration FCC_TEST_DATABASE_URL='postgres://user:pass@host:5432/scratch?sslmode=disable'
```

Expect 8 integration tests plus the `internal/db` suite. A `SKIP` means the
database was unreachable — that is a failure to run, not a pass.

## Deployment

- Container: [`deploy/Dockerfile`](deploy/Dockerfile) — static Go binary on
  distroless, non-root, ~15 MB image.
- AWS Terraform module: [`deploy/terraform/aws/`](deploy/terraform/aws/) —
  Aurora Postgres (Serverless v2) + ECS Fargate + ALB + KMS + Secrets
  Manager reference.

## Specs this implements

- [canonical-message.md](../../spec/canonical-message.md) — the wire format
  consumed on `/v1/ingest`
- [parser-grammar.md](../../spec/parser-grammar.md) — the command grammar
- [ai-teammate-protocol.md](../../spec/ai-teammate-protocol.md) — the
  `@ai-teammate` protocol (echo back / recall shapes)
- [transport-adapter.md](../../spec/transport-adapter.md) — the outbound
  workflow forward shape
