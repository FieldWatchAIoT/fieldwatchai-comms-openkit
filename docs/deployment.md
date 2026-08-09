# Deployment

**Status:** placeholder. Full recipes are targeted for **Q4 2026**, alongside
the first cut of reference implementation code in [`../examples/`](../examples/).

This document describes the deployment shape adopters should plan for. When
the reference recipes ship, this file will be replaced with links to
step-by-step guides.

## Three target environments

The reference deployment story covers three environments, in increasing
order of operational complexity.

### 1. Local — `docker-compose`

**Target adopter:** a developer or a small NGO trying the OpenKit on a
laptop, or a training environment for operators.

**Coming Q4 2026.** Will include:

- `docker-compose.yml` running: the inbound webhook receiver, the
  channels service, a Postgres 16 + PostGIS database, an in-memory
  queue (no external cloud queue needed for local).
- `.env.example` with all required environment variables and a note
  next to each explaining what it is and how to generate a value.
- A one-command bring-up: `docker compose up --build`.
- A smoke-test script that fires a canned webhook payload at the
  local receiver and verifies it reaches the channels service.

### 2. AWS ECS reference deployment

**Target adopter:** an agency that has decided to run on AWS (this is
what FieldWatch itself runs, so the guide will reflect the same
architecture).

**Coming Q4 2026.** Will include:

- Terraform modules for: an ALB, an ECS Fargate service (private
  subnets, no public IP), SQS + a DLQ (encrypted with a KMS CMK),
  Secrets Manager entries for the per-adapter secrets, CloudWatch
  Logs + metric filters keyed on the standard observability events,
  VPC interface endpoints for SQS / Secrets Manager / CloudWatch
  Logs / ECR.
- An OIDC deployment role and a GitHub Actions workflow file that
  builds the container image, pushes to ECR with an immutable tag,
  and updates the ECS service.
- A worked example of a `task-definition.json` with the secrets
  block pulling from Secrets Manager.
- Sizing guidance and a rough per-month cost estimate for a Bahamas-
  scale deployment (low-traffic normal load with peak surge during
  a named-storm event).

### 3. Self-hosted Kubernetes

**Target adopter:** an agency that runs its own on-prem infrastructure
or a non-AWS cloud, for data-residency or sovereignty reasons.

**Coming Q1 2027.** Will include:

- Helm chart(s) for the inbound receiver, the channels service, and
  the database (though most adopters will bring their own managed
  Postgres).
- Guidance for durable queueing without SQS: RabbitMQ, Redis Streams,
  and NATS JetStream will each get a note about tradeoffs and a
  minimal config.
- A worked example of ingress + TLS termination that is provider-
  agnostic.

## Cross-cutting concerns

### Secrets

Never bake secrets into container images. The reference deployments
inject secrets from a secrets manager (Secrets Manager on AWS,
sealed-secrets or an external-secrets operator on Kubernetes, an
env file mounted from a private path on `docker-compose`).

### TLS

All inbound webhook endpoints must be behind TLS. Verify the platform's
webhook signing matches the exact public URL the platform is signing
against — behind a load balancer, the internal request URL is usually
not what the platform signed.

### Health checks

The reference implementations expose `/healthz`. Load balancers should
probe every 30 seconds with a short deregistration delay (~30 s) so
rolling deploys are graceful.

### Observability

The reference implementations emit structured JSON logs to stdout,
keyed on the standard `event` values described in
[`../spec/transport-adapter.md`](../spec/transport-adapter.md). Set up
metric filters and alarms on `invalid_signature` (drives the
security-alerting story) and on DLQ depth.

### Backups and recovery

The channels service owns the database. Nightly backups are the
minimum; point-in-time recovery is strongly recommended for any
production deployment during storm season.

## Air-gap / offline consideration

Some adopters operate in environments where internet connectivity is
intermittent — before a storm's landfall, satellite links are the
only option; on the ground during recovery, cellular can be down for
days. The specifications are designed to support:

- **Buffer-and-forward** — the inbound receiver accepts, durably
  buffers, and forwards to the channels service. A channels-side
  outage never drops an inbound field message.
- **Satellite messenger integration** — on the roadmap in Q1 2027
  (ZOLEO, Garmin inReach, SPOT, Iridium SBD).

A specific air-gap deployment recipe (self-contained on-prem stack
with a manual message-relay bridge) is not on the near roadmap but
is a candidate for community contribution.

---

## Timelines are targets

If a small team of Bahamian engineers cannot credibly hit a date on
this page, we will move the date rather than ship a half-built
recipe that leaves an adopter stranded during an actual event.

If you are planning a deployment and any of the dates above matter
to your planning, please open an issue with your timeline. Concrete
adopter timelines help us prioritize.
