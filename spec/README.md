# Specifications

This folder contains the normative specifications for the FieldWatch Comms
OpenKit. Anything published here is intended to be implementable — a person or
team building against these documents alone, without access to FieldWatch's
private code, should be able to build a conforming system.

## Contents

| Spec | Purpose | Status |
|---|---|---|
| [`canonical-message.md`](./canonical-message.md) | The wire format inbound platform messages get normalized into. | v1 |
| [`canonical-message.schema.json`](./canonical-message.schema.json) | JSON Schema (draft 2020-12) for the canonical message, for automated validation. | v1 |
| [`parser-grammar.md`](./parser-grammar.md) | The rule-based grammar for extracting `SHORT_ID`, target, command, and payload from a canonical message body. | v1 |
| [`ai-teammate-protocol.md`](./ai-teammate-protocol.md) | How an AI participant plugs into a comms channel as a first-class team member — summoned, yielding, read-first, auditable. | v1 draft |
| [`transport-adapter.md`](./transport-adapter.md) | The contract every inbound / outbound platform adapter must implement to be considered conforming. | v1 |

## How these fit together

```
platform-native inbound
      │
      ▼
transport adapter  ──► canonical message  ──► parser  ──► routing + policy
(spec/transport-      (spec/canonical-        (spec/                (out of scope
 adapter.md)           message.md)             parser-               of this repo —
                                               grammar.md)           belongs to
                                                    │                the consumer)
                                                    ▼
                                            AI teammate (optional)
                                            (spec/ai-teammate-
                                             protocol.md)
```

## Versioning

Specifications are versioned independently. A version bump on any spec
document uses `MAJOR.MINOR`:

- **MINOR** — additive, non-breaking. Old conforming implementations remain
  conforming. New optional fields, new enum values that clients can ignore,
  new example messages, clarifications.
- **MAJOR** — breaking. A previously conforming implementation may need to
  change to remain conforming. This is deliberately expensive to do and
  requires a Spec Change Proposal issue (see the `.github/ISSUE_TEMPLATE/`).

Every spec file states its current version at the top.

## How to propose a change

Open a **Spec change proposal** issue using the template in
`.github/ISSUE_TEMPLATE/`. Spec changes get discussed first and turned into a
PR second — the reverse of the normal contribution flow.
