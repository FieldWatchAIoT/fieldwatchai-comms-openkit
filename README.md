# FieldWatch Comms OpenKit

**An open protocol and reference implementations for disaster-response communications infrastructure.**

Built in the Bahamas by [FieldWatch AI](https://fieldwatch.earth/). Licensed Apache 2.0. Being prepared as a Digital Public Good.

---

## What this is

The Comms OpenKit is:

1. **A specification** — the message schema, command grammar, transport adapter contract, and AI-teammate protocol that FieldWatch's own comms hub runs on.
2. **A set of reference implementations** — starter code showing how to plug WhatsApp, SMS, Telegram, email, and (roadmap) satellite messengers into a disaster-response comms stack.
3. **A deployment recipe** — how any disaster-management agency in a small island developing state (SIDS) can stand up the same coordination surface FieldWatch runs, on their own infrastructure, without licensing a commercial product.

Any group — a National Emergency Management Agency (NEMA), a Red Cross chapter, a mutual-aid network, a private search-and-rescue team — can implement the spec and interoperate with the reference stack.

## Why this exists

In September 2019, Hurricane Dorian sat over the northern Bahamas as a Category 5 storm for roughly 40 hours. 74 people were confirmed dead. Around 245 people are still officially missing, seven years on.

The failure was not a lack of goodwill. Responders showed up from all over the world. The failure was coordination. Radio, WhatsApp, SMS, email, Facebook posts, family group chats, and paper lists were all being used simultaneously by people who could not see each other's traffic. Messages were duplicated, contradicted, or lost. Missing-person reports arrived days after they might have helped.

Commercial disaster-comms platforms exist. They cost hundreds of thousands of US dollars per year, per agency. No Caribbean government can license one for every department that needs it, and no small NGO can license one at all. The gap gets papered over with consumer messaging apps that were never designed for life-safety traffic and that share no common schema.

The Comms OpenKit is an attempt to close that gap with open protocol and open code that a country the size of the Bahamas — or Dominica, or Vanuatu, or any comparable jurisdiction — can adopt and run itself.

## Who this is for

- **Caribbean NEMAs and equivalent agencies** in small island developing states.
- **Governments and civil-defense bodies** in climate-vulnerable jurisdictions that need coordination infrastructure they can own outright.
- **Disaster-response NGOs** — Red Cross societies, mutual-aid networks, search-and-rescue teams — that need to interoperate across radios, WhatsApp, SMS, Telegram, email, and satellite messengers without a proprietary hub.
- **Developers** building communications tools for climate-vulnerable communities who want a reference protocol to interoperate against.
- **Researchers and standards bodies** working on humanitarian communications interoperability.

## Quick start — full stack in one command

Runnable in one command. Requires Docker + Compose.

```sh
git clone https://github.com/FieldWatchAIoT/fieldwatchai-comms-openkit.git
cd fieldwatchai-comms-openkit
docker compose up --build
```

That brings up the full stack — webhook + channels + Postgres + LocalStack
SQS — wired together end-to-end:

- `webhook` on `localhost:8080` — the inbound receiver
- `channels` on `localhost:9090` — the routing brain (owns the DB)
- `postgres` on `localhost:5434` — Postgres 16 + PostGIS
- `localstack` on `localhost:4566` — SQS
- `seed` — a one-shot container that inserts the demo tenant, account, and
  contact, then exits (see [`deploy/seed/`](./deploy/seed/))

Send a synthetic WhatsApp payload through the whole pipeline:

```sh
curl -X POST 'http://localhost:8080/inbound/whatsapp-ultramsg?token=local-secret' \
     -H 'Content-Type: application/json' \
     --data @implementations/webhook-go/internal/listeners/ultramsg/testdata/text.json

# and see it land in the channels DB
docker compose exec postgres \
  psql -U openkit -d openkit \
  -c 'SELECT direction, body_text, received_at FROM messages ORDER BY received_at DESC LIMIT 5;'
```

You should get one `inbound` row reading `42 STATUS full`. To see what the
routing brain actually decided about it:

```sh
docker compose exec postgres psql -U openkit -d openkit -c \
  "SELECT policy_action, parsed->>'command' AS cmd, parsed->>'confidence' AS confidence
     FROM messages ORDER BY received_at DESC LIMIT 1;"
# policy_action | cmd    | confidence
# execute       | STATUS | 1
```

`execute` means the parser read the command, the resolver matched short-id `42`
to the seeded contact at full confidence, and the policy gate cleared it to run.
Delete that contact and the same message comes back as `clarify` instead — that
is the confidence gate doing its job, not a failure.

### What the demo seeds, and why it is needed

Every inbound message is resolved to an account before anything else happens: the
webhook asks channels `GET /v1/accounts/lookup`, and a miss means the message is
acknowledged and dropped. So an empty database silently discards everything. The
`seed` service inserts three rows to get past that:

| Row | Value | Why |
|---|---|---|
| tenant | `11111111-…-111111111111` | `accounts.tenant_id` is a foreign key; there is no tenant API |
| account | type `whatsapp`, identifier `instance123` | matches the `instanceId` in the test payload |
| contact | short-id `42` | makes the demo message resolve at full confidence |

Two things routinely catch people out here:

- **The account type is `whatsapp`, not `whatsapp-ultramsg`.** The webhook sends
  its *listener id* (`whatsapp-ultramsg`) to the lookup, and channels maps that to
  the stored *platform type* (`whatsapp`). Store the listener id and every lookup
  misses, with no error anywhere.
- **Tenants can only be created in SQL.** Tenancy is provisioned out of band, so
  channels exposes no tenant endpoint. `POST /v1/accounts` against a tenant id
  that does not exist fails the foreign key and returns a bare `internal_error`.

Accounts and contacts *do* have a full CRUD API — the seed uses SQL only so it
can create the tenant in the same round trip. The API equivalents:

```sh
curl -X POST http://localhost:9090/v1/accounts \
  -H 'Authorization: Bearer local-token' \
  -H 'X-Tenant-ID: 11111111-1111-1111-1111-111111111111' \
  -H 'Content-Type: application/json' \
  -d '{"type":"whatsapp","owner_type":"tenant","label":"Demo WhatsApp",
       "platform_identifier":"instance123","status":"active",
       "capabilities":["inbound","outbound"]}'

curl -X POST http://localhost:9090/v1/contacts \
  -H 'Authorization: Bearer local-token' \
  -H 'X-Tenant-ID: 11111111-1111-1111-1111-111111111111' \
  -H 'Content-Type: application/json' \
  -d '{"short_id":"42","display_name":"Marsh Harbour Shelter","status":"active"}'
```

Outbound replies need real platform credentials on the account
(`credentials` on the create call). The seeded account has none, so inbound and
routing work offline but an outbound send will fail until you add them.

Want to run the two services individually instead? Each implementation folder
has its own `docker-compose.yml` for isolated dev:

- [`implementations/webhook-go/`](./implementations/webhook-go/) — webhook +
  a `fakechannels` stub, no DB
- [`implementations/channels-go/`](./implementations/channels-go/) — channels +
  Postgres, no webhook

For a real deployment, each implementation ships a Terraform module:

- [`implementations/webhook-go/deploy/terraform/aws/`](./implementations/webhook-go/deploy/terraform/aws/) —
  ECS Fargate + ALB + SQS (~$25-30/mo)
- [`implementations/channels-go/deploy/terraform/aws/`](./implementations/channels-go/deploy/terraform/aws/) —
  Aurora Postgres + ECS Fargate + ALB + KMS + Secrets Manager (~$100-150/mo)

## What is in the box

| Component | Status | Location |
|---|---|---|
| Apache 2.0 license | shipped | [`LICENSE`](./LICENSE) |
| Canonical message schema (JSON Schema draft-2020-12) | shipped | [`spec/canonical-message.schema.json`](./spec/canonical-message.schema.json) |
| Canonical message spec (human-readable) | shipped | [`spec/canonical-message.md`](./spec/canonical-message.md) |
| Rule-based command parser grammar | shipped | [`spec/parser-grammar.md`](./spec/parser-grammar.md) |
| AI-teammate protocol (v1 draft) | shipped | [`spec/ai-teammate-protocol.md`](./spec/ai-teammate-protocol.md) |
| Transport adapter contract | shipped | [`spec/transport-adapter.md`](./spec/transport-adapter.md) |
| Digital Public Goods alignment mapping | shipped | [`docs/dpg-alignment.md`](./docs/dpg-alignment.md) |
| DPG manifest | shipped | [`dpg-manifest.yml`](./dpg-manifest.yml) |
| Deployment guide (docker-compose, ECS, k8s) | shipped | [`docs/deployment.md`](./docs/deployment.md) |
| Go webhook reference implementation (inbound) | shipped | [`implementations/webhook-go/`](./implementations/webhook-go/) |
| Go channels reference implementation (routing brain + DB + outbound) | shipped | [`implementations/channels-go/`](./implementations/channels-go/) |
| Full-stack root `docker-compose.yml` (webhook + channels + Postgres + LocalStack) | shipped | [`docker-compose.yml`](./docker-compose.yml) |
| Reference listener: UltraMSG WhatsApp (inbound) | shipped | [`implementations/webhook-go/internal/listeners/ultramsg/`](./implementations/webhook-go/internal/listeners/ultramsg/) |
| Reference listener: Twilio SMS + WhatsApp (inbound) | shipped | [`implementations/webhook-go/internal/listeners/twilio/`](./implementations/webhook-go/internal/listeners/twilio/) |
| Reference listener: Telegram Bot API (inbound) | shipped | [`implementations/webhook-go/internal/listeners/telegram/`](./implementations/webhook-go/internal/listeners/telegram/) |
| Reference listener: AWS SES email (inbound) | shipped | [`implementations/webhook-go/internal/listeners/email/`](./implementations/webhook-go/internal/listeners/email/) |
| Reference integration: UltraMSG WhatsApp (outbound) | shipped | [`implementations/channels-go/internal/integrations/ultramsg/`](./implementations/channels-go/internal/integrations/ultramsg/) |
| Reference integration: Twilio SMS + WhatsApp (outbound) | shipped | [`implementations/channels-go/internal/integrations/twilio/`](./implementations/channels-go/internal/integrations/twilio/) |
| Reference integration: Telegram (outbound) | shipped | [`implementations/channels-go/internal/integrations/telegram/`](./implementations/channels-go/internal/integrations/telegram/) |
| Reference integration: AWS SES v2 email (outbound) | shipped | [`implementations/channels-go/internal/integrations/emailses/`](./implementations/channels-go/internal/integrations/emailses/) |
| AWS Terraform module — webhook (ECS Fargate + ALB + SQS) | shipped | [`implementations/webhook-go/deploy/terraform/aws/`](./implementations/webhook-go/deploy/terraform/aws/) |
| AWS Terraform module — channels (Aurora Postgres + ECS Fargate + ALB + KMS) | shipped | [`implementations/channels-go/deploy/terraform/aws/`](./implementations/channels-go/deploy/terraform/aws/) |
| Satellite adapters (ZOLEO, inReach, SPOT, Iridium SBD) | on the roadmap | — |

## Digital Public Goods alignment

The Digital Public Goods Alliance defines a DPG as open-source software, data, AI models, standards, or content that helps achieve the Sustainable Development Goals. This project targets:

- **SDG 11** — Sustainable Cities and Communities (target 11.5: reduce deaths and losses from disasters)
- **SDG 13** — Climate Action (target 13.1: strengthen resilience and adaptive capacity)
- **SDG 17** — Partnerships (target 17.6: technology cooperation)

A full mapping against the 9 DPG Standard indicators lives in [`docs/dpg-alignment.md`](./docs/dpg-alignment.md). Registry submission is targeted for Q1 2027.

## Roadmap

- **Q3 2026 (shipped)** — publish the specs: canonical message, parser grammar, AI-teammate protocol, transport adapter contract. Publish the Go reference inbound webhook (UltraMSG, Twilio, Telegram, SES) with a runnable `docker-compose` recipe and a minimal AWS Terraform deployment.
- **Q3 2026 (shipped)** — publish the Go channels reference (the routing brain, Postgres+PostGIS schema, parser + policy + outbound dispatch + workflow forwarding) with its own AWS Terraform module (Aurora Postgres + KMS) and a root `docker-compose` that boots the full stack end-to-end.
- **Q1 2027** — submit to the Digital Public Goods Alliance registry. Publish Kubernetes deployment recipe. First satellite messenger adapter (ZOLEO).
- **Q2 2027** — voice channel spec (Vapi / ElevenLabs pattern). First adopter deployment outside FieldWatch.

Dates are targets. If a small team of Bahamian engineers can't credibly hit a date, we will move the date rather than ship something half-built. This project has to work when a storm actually lands.

## Status

**Early public release.** The specifications in `spec/` are the ones FieldWatch's own comms hub implements today, so they are already load-bearing internally. The Go reference implementations in `implementations/webhook-go/` and `implementations/channels-go/` are extracted from that same internal codebase and run against real inbound + outbound traffic there; the AWS Terraform modules ship as reference deployments (not hardened landing zones — read their READMEs before applying).

The best way to influence the direction is to open an issue with your use case — especially if you work in a Caribbean or Pacific SIDS disaster-management context, or a comparable resource-constrained jurisdiction.

## Contributing

See [`CONTRIBUTING.md`](./CONTRIBUTING.md). Contributions from Caribbean and Pacific SIDS engineers are especially welcome — this is your infrastructure as much as anyone's.

Security issues: please do not open a public issue. See [`SECURITY.md`](./SECURITY.md).

Conduct: see [`CODE_OF_CONDUCT.md`](./CODE_OF_CONDUCT.md).

## License

Apache License 2.0. See [`LICENSE`](./LICENSE).

## Attribution and origin

The Comms OpenKit is developed and maintained by [FieldWatch AI](https://fieldwatch.earth/), a company based in Nassau, The Bahamas, and founded after Hurricane Dorian.

This work is being carried out in part under a commitment to the UNFCCC AI for Climate Action Award programme. The award recognizes the intent to open-source the protocol and reference implementations underlying FieldWatch's disaster-response coordination product, so that any climate-vulnerable jurisdiction can adopt the same infrastructure without paying for it.

If your agency, NGO, or research group deploys this stack, we would love to hear about it — even a one-line email to `hello@fieldwatchai.io` helps us make the case that open protocols for humanitarian comms are worth investing in.
