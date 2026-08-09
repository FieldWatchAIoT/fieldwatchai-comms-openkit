---
name: Spec change proposal
about: Propose a change to the canonical message schema, parser grammar, AI-teammate protocol, or transport adapter contract
title: "[spec] "
labels: spec-change
assignees: ''
---

Changes to anything in `spec/` are load-bearing for adopters. Please give this
proposal enough context that a reviewer who has never met you can decide
whether to accept it.

## Which spec

- [ ] `spec/canonical-message.md` / `spec/canonical-message.schema.json`
- [ ] `spec/parser-grammar.md`
- [ ] `spec/ai-teammate-protocol.md`
- [ ] `spec/transport-adapter.md`
- [ ] Other (please describe)

## The proposal in one sentence

## The motivation

What real problem does this solve? If possible, describe the concrete situation
(the platform, the incident, the operator experience) that surfaced it.

## The proposed change (detailed)

Show, don't just tell. If you are changing a schema field, write the before
and after. If you are changing the grammar, write example messages and how
they would be parsed under the new rule.

**Before:**

```
(old schema / grammar / prose)
```

**After:**

```
(new schema / grammar / prose)
```

## Compatibility

- Is this a **breaking change** for existing adopters? (A change that would
  cause a spec-conforming implementation to reject or misinterpret messages it
  used to handle.)
- If yes: what is the migration path? Can it be introduced as an additive
  change first and then made required?
- Is there a version-negotiation story?

## Alternatives considered

## Who else should weigh in

Are there specific implementers, agencies, platforms, or standards bodies
whose input should shape this before it merges?
