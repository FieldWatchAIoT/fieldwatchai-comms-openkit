# Digital Public Goods Alliance — alignment

This document maps the FieldWatch Comms OpenKit to the 9 indicators the
Digital Public Goods Alliance uses to evaluate submissions to the DPG
Registry (v1.0 of the DPG Standard, as published at
<https://digitalpublicgoods.net/standard/>).

The mapping is honest. Where we are not there yet, we say so, and we
link to the tracking work.

**Registry submission target:** Q1 2027.

**Legend:** ✅ complete · 🟡 in progress · ⬜ planned but not started

---

## 1. Relevance to Sustainable Development Goals

**Status:** ✅ complete

The project explicitly targets:

- **SDG 11 — Sustainable Cities and Communities**, target **11.5**: "By
  2030, significantly reduce the number of deaths and the number of
  people affected and substantially decrease the direct economic losses
  … caused by disasters, including water-related disasters, with a
  focus on protecting the poor and people in vulnerable situations."

- **SDG 13 — Climate Action**, target **13.1**: "Strengthen resilience
  and adaptive capacity to climate-related hazards and natural
  disasters in all countries."

- **SDG 17 — Partnerships for the Goals**, target **17.6**:
  "Enhance … cooperation on and access to science, technology and
  innovation and enhance knowledge sharing …"

The founding narrative is Hurricane Dorian (Bahamas, Sept 2019): 74
confirmed dead, ~245 still missing, a coordination failure across
consumer messaging tools that no single agency could unify. Small
island developing states cannot afford enterprise disaster-comms
tooling; open protocol is the only path.

## 2. Use of an approved open license

**Status:** ✅ complete

Apache License 2.0 — an OSI-approved, DPG-approved license — governs
everything in this repository. See [`../LICENSE`](../LICENSE).

## 3. Clear ownership

**Status:** ✅ complete

- **Copyright owner** for original contributions: FieldWatch AI
  (Bahamas company).
- **Trademark:** FieldWatch AI does not assert any trademark over the
  specifications or the reference implementations.
- **Governance:** currently benevolent-maintainer, with contributions
  accepted under the Developer Certificate of Origin (see
  [`../CONTRIBUTING.md`](../CONTRIBUTING.md)). A more formal
  governance model will be adopted as the contributor base grows.
- **Contact:** `command@fieldwatchai.io`.

## 4. Platform independence

**Status:** 🟡 in progress

- **Specifications** are platform-independent by construction — they
  describe wire formats and behavioral contracts, not any particular
  runtime.
- **Reference implementations** (targeted for Q4 2026) will be
  written in **Go** with **stdlib `net/http`** to minimize the
  dependency footprint. The build produces a static binary that runs
  on Linux (any amd64 or arm64), macOS, and Windows.
- **Deployment recipes** will cover: local docker-compose (any
  container runtime), AWS ECS (as a reference cloud deployment,
  because that is what FieldWatch runs), and self-hosted Kubernetes.
  None of the specifications require any cloud-specific service.

Anything that ends up cloud-specific in the reference implementation
(e.g. AWS SQS as the durable buffer) will be abstracted behind a
narrow interface so an equivalent (e.g. RabbitMQ, Redis Streams,
Postgres LISTEN/NOTIFY) can be swapped in for adopters running
elsewhere.

## 5. Documentation

**Status:** 🟡 in progress

Documentation already in this repository:

- Project overview ([`../README.md`](../README.md))
- Specifications ([`../spec/`](../spec/)): canonical message
  (with JSON Schema), parser grammar, AI-teammate protocol,
  transport adapter contract
- Contribution guidance ([`../CONTRIBUTING.md`](../CONTRIBUTING.md))
- Code of conduct ([`../CODE_OF_CONDUCT.md`](../CODE_OF_CONDUCT.md))
- Security policy ([`../SECURITY.md`](../SECURITY.md))

Documentation still to come:

- Deployment guides — [`./deployment.md`](./deployment.md) (Q4 2026)
- Per-adapter READMEs will fill out as the reference implementations
  land ([`../examples/`](../examples/)).
- End-to-end incident-time operator playbook (planned Q1 2027, drawn
  from FieldWatch's own operational experience).

## 6. Mechanism for extracting data

**Status:** 🟡 in progress

The canonical message format is JSON and includes the full raw
platform payload (`meta.raw_payload`), so an adopter always has
enough information to reconstruct or export the underlying
communications. The reference implementation will expose:

- Structured export of message history in JSON Lines.
- A read-only API to retrieve stored messages by tenant, channel,
  and time range.
- No proprietary storage formats.

## 7. Adherence to privacy and applicable laws

**Status:** 🟡 in progress

The specifications are designed to be deployable in a way that
complies with common privacy regimes (GDPR-style principles: purpose
limitation, data minimization, retention limits, subject-access
requests). Specific commitments the reference implementations will
uphold:

- **Data minimization** — the canonical message carries only what
  the platform delivered. Adapters do not enrich, geolocate, or
  profile.
- **No re-hosting of media** — attachment URLs point at platform
  hosting; the OpenKit does not copy media into its own storage.
- **Redaction hooks** — the storage layer will support tenant-defined
  retention windows and hard-delete requests.
- **PII in logs** — reference implementations do not log message
  bodies at INFO or higher levels; secrets are redacted from config
  logs.
- **Cross-border data flows** — the deployment guides will call out
  the data-residency implications of each cloud recipe.

A dedicated privacy and data-handling document will land alongside the
first reference implementation in Q4 2026.

## 8. Adherence to standards and best practices

**Status:** ✅ complete for what is shipped

- **JSON Schema** — the canonical message schema is published as
  JSON Schema draft-2020-12.
- **RFC 3339** for timestamps.
- **E.164** for phone numbers.
- **RFC 5321** for email mailbox format.
- **HTTP** for transport, with idempotency via the
  `Idempotency-Key` header pattern.
- **Constant-time comparison** for all secret verification paths in
  the reference adapters.
- **Apache 2.0 open source license.**
- **Semantic Versioning** for spec version numbers.

## 9. Do no harm by design

**Status:** 🟡 in progress

The most sensitive design surface here is the AI-teammate protocol,
which is why it is being published as a draft ahead of stabilization
and is explicitly asking for outside review. Concrete do-no-harm
commitments already built into the specifications:

- **Rule-based command parser** is deliberately deterministic. An
  LLM does not sit on the hot path deciding what a message means. If
  a rule-based interpretation cannot be made confidently, the
  message routes to a human, not to a model that will improvise.
- **AI teammates are silent by default**, summoned only, always
  yield to humans, and any participant can silence them at any
  time. Every action is audit-logged.
- **AI teammates cannot take write actions in v1** — no autonomous
  world-changing actions during a disaster response.
- **Verification is fail-closed** everywhere; a webhook that cannot
  be verified is rejected, not accepted-with-a-warning.
- **No re-hosting** of media puts hosting liability where the media
  actually lives.
- **Deliberate drops return HTTP 200** — the specification
  discourages upstream retry storms that would degrade platforms
  during an event.

A dedicated harms-analysis document, covering misuse scenarios
(coordinated disinformation, harassment via SOS-flooding, adversarial
model prompts), is on the roadmap for Q1 2027.

---

## Summary

| Indicator | Status |
|---|---|
| 1. SDG relevance | ✅ |
| 2. Open license | ✅ |
| 3. Clear ownership | ✅ |
| 4. Platform independence | 🟡 |
| 5. Documentation | 🟡 |
| 6. Mechanism for extracting data | 🟡 |
| 7. Privacy and applicable laws | 🟡 |
| 8. Standards and best practices | ✅ |
| 9. Do no harm by design | 🟡 |

Four of nine are complete for what is shipped; the remainder are in
active work. All nine are targeted to be closable by Q1 2027, in time
for a registry submission.

If you are a DPG Alliance evaluator, please email
`command@fieldwatchai.io` — we would rather receive early feedback than
wait until submission.
