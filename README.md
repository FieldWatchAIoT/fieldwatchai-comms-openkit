# FieldWatch Comms OpenKit

**A switchboard for disaster-response messaging.** Field reports arrive over
WhatsApp, SMS, Telegram, or email; they come out the other side as one stream of
structured, permanently recorded messages your own systems can read.

Built in the Bahamas by [FieldWatch AI](https://fieldwatch.earth/). Licensed Apache 2.0. Being prepared as a Digital Public Good.

---

## What this is

In a disaster, people use whatever still has signal and whatever is already on
their phone. You cannot make a frightened person in a flooded house install a
new app. So responders end up spread across WhatsApp, SMS, email, Telegram, and
paper, unable to see each other's traffic.

This project takes the opposite approach: **let everyone keep the app they
already have, and put the switchboard behind them.** Every message lands in one
place, in one format, in one permanent record.

The closest everyday analogy is email. You have never had to care whether the
person you are writing to uses Gmail or Outlook — the message simply arrives.
This does that for disaster traffic.

### What happens to a single message

A shelter manager texts **`42 STATUS full`** over WhatsApp:

| Step | What happens |
|---|---|
| **Arrives** | The sender is verified before anything else — an HMAC signature where the platform offers one (Twilio), a constant-time shared-secret check where it does not (UltraMSG, Telegram, SES). Verification is fail-closed. |
| **Translated** | WhatsApp, SMS, Telegram, and email deliver wildly different payloads. Each is rewritten into one [canonical message](./spec/canonical-message.md), so everything downstream sees the same shape. |
| **Understood** | `42` is looked up in the address book — Marsh Harbour Shelter. `STATUS` is the command. `full` is the detail. |
| **Judged** | The match is scored. Exact hit here, so it passes straight through as `execute`. |
| **Filed** | Stored permanently: the original payload, the timestamp, and what the system understood. |
| **Handed off** | Posted to whatever system you already run — your dashboard, your map, your database. |

**When it is not sure, it does not guess.** On a partial match it replies
*"Did you mean Marsh Harbour Shelter?"* and waits. The sender can confirm,
correct, or text `OOPS` to cancel what they just sent, within a time window you
set. A confident wrong guess sends a boat to the wrong island; asking is
cheaper than being wrong.

Relatedly, and deliberately: **no AI decides what a message means.** Parsing is
rule-based and deterministic, so the same message always resolves the same way.
There is an [AI-teammate protocol](./spec/ai-teammate-protocol.md) for AI
participants, and it is constrained — silent by default, summoned only,
read-only, always yielding to humans.

## What this is NOT

Read this part before you plan around it.

- **It is not an operator app.** There is a small read-only console at
  `/console` for confirming traffic is arriving and being understood — no map
  view, no case management, nothing a coordinator would run a response from.
  This is the plumbing that sits underneath such a tool. Everything else is an
  HTTP API and a database.
- **It is not turnkey.** Adopting it requires a developer — someone who can run
  containers, hold a Postgres database, and build the interface your staff will
  actually use. Budget for that before committing.
- **It is not a replacement for radio or official alerting.** It handles
  two-way field traffic. It does not do public warning broadcast (CAP/CBC),
  and it is not a life-safety system of record on its own.
- **It does not host or analyze media.** Photos and documents are passed
  through as links to the platform's own storage.
- **No satellite or voice yet.** Satellite messengers (for when the towers are
  down) and voice are on the roadmap, not in the box.

## What you get

1. **A specification** — the message schema, command grammar, transport adapter contract, and AI-teammate protocol that FieldWatch's own comms hub runs on.
2. **Two working Go services** — an inbound receiver and a routing brain, extracted from that same production codebase. Not pseudocode; they run.
3. **A deployment recipe** — docker-compose for a laptop, Terraform for AWS, so an agency can stand up the same coordination surface on infrastructure it owns.

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

Every one of those routes through a developer. The people this ultimately
serves — the shelter manager texting `42 STATUS full`, the family reporting
someone missing — never touch this repository and never know it exists. That is
the intended outcome, not a gap.

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

Open **<http://localhost:9090/console>** to watch traffic arrive.

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

## Setting up your own deployment

The demo seeds itself. For a real deployment — your own WhatsApp number, your
own address book — one command wires everything:

```sh
cd implementations/channels-go
make setup
```

It asks for your platform details and creates the four things a working
deployment needs, in order: a **tenant**, an **account** (the inbox messages
arrive on), a **channel** (where traffic is routed, and what carries your
`workflow_url`), and the **link** between account and channel. Then it verifies
the result and prints the ids you will need.

That last step matters more than it sounds. An account with no channel link
still accepts messages, parses them, and stores them — it just forwards them
nowhere, silently, returning `200` the whole time. It is the single most common
way a new deployment appears to work while doing nothing.

You can ask the service about that state at any time:

```sh
curl -s localhost:9090/v1/diagnostics \
  -H 'Authorization: Bearer local-token' \
  -H 'X-Tenant-ID: <your-tenant-id>'
```

It reports what exists and names what is missing, with the command to fix each
one:

```json
{
  "healthy": false,
  "counts": { "accounts": 1, "channels": 0, "contacts": 0, "messages": 0 },
  "findings": [
    {
      "severity": "blocking",
      "code": "account_not_linked_to_channel",
      "summary": "Account \"Demo WhatsApp\" (whatsapp instance123) is not linked to any inbound channel. Messages arriving on it are stored and forwarded nowhere.",
      "remedy": "POST /v1/channels to create a channel, then POST /v1/channels/{id}/accounts with account_id=... and direction=\"both\"."
    }
  ]
}
```

`blocking` means traffic is being discarded or stranded. `warning` means it
flows but something downstream is missing — a channel with no `workflow_url` is
perfectly valid if you read the database directly.

Everything `make setup` does is an ordinary API call, so you can script it
instead. No step requires SQL.

### Watching it work

`http://localhost:9090/console` is a single read-only page showing whether the
system is receiving, and what it made of each message:

| Column | What it tells you |
|---|---|
| Message | Exactly what the sender typed |
| Understood as | The short id and command the parser resolved, and how confident it was |
| Outcome | `acted on`, `confirming`, `needs reply`, or `withdrawn` |
| Your system | Whether the forward to your `workflow_url` was delivered |

When something is misconfigured it says so in the same words as
`/v1/diagnostics` — *"Account "Demo WhatsApp" is not linked to any inbound
channel. Messages arriving on it are stored and forwarded nowhere"* — with the
command to fix it.

It loads no external resources: no CDN, no webfont, no framework. A console
that needs the internet is broken exactly when it is needed most. It authenticates
with the same `INTERNAL_API_TOKEN` as the API, held in the browser tab only.

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
| Privacy and data-handling document | shipped | [`docs/privacy.md`](./docs/privacy.md) |
| Harms analysis (do-no-harm assessment) | shipped | [`docs/harms-analysis.md`](./docs/harms-analysis.md) |
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

A full mapping against the 9 DPG Standard indicators lives in
[`docs/dpg-alignment.md`](./docs/dpg-alignment.md), alongside a
[privacy and data-handling document](./docs/privacy.md) and a
[harms analysis](./docs/harms-analysis.md). All nine indicators are met for what
is shipped; the work that remains is named rather than glossed. Registry
submission is targeted for Q1 2027.

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
