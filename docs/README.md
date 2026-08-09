# Documentation

Deployment guides, alignment mappings, and design background — anything that
is not a normative specification (those live in [`../spec/`](../spec/)) and
not runnable example code (those live in [`../examples/`](../examples/)).

## Contents

| Document | Purpose | Status |
|---|---|---|
| [`dpg-alignment.md`](./dpg-alignment.md) | Mapping this project to the 9 Digital Public Goods Alliance criteria. Honest checklist — each item is marked complete, in progress, or planned. | living document |
| [`deployment.md`](./deployment.md) | Deployment guidance for adopters — local docker-compose, AWS ECS reference, self-hosted Kubernetes recipe. | placeholder; content targeted Q4 2026 |

## Guidance for adding a document here

Documents in this folder should be either:

- **Reference** — how the OpenKit maps to an external framework
  (DPG Alliance, a specific SDG, a standards body's checklist).
- **Guidance** — how to actually run the thing (deployment,
  operations, incident-time playbooks).

Design documents that argue for *why* the specs look the way they do
should live in the individual spec files (as their "Design principles"
section), not here — that way an implementer reading a spec has the
rationale next to the rule.

Anything specific to a single reference implementation belongs in that
implementation's `examples/` folder, not here.
