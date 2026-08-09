# AI Teammate Protocol

**Version:** 1.0-draft
**Status:** draft — the first spec here that is deliberately being published
before it is fully hardened, because getting outside review on the design
matters more than shipping a settled version.
**Applies to:** any AI participant (large-language-model based or otherwise)
that joins a comms channel governed by this spec suite.

## The idea in one sentence

An AI teammate is an AI participant that sits inside a comms channel like
another human on the roster — summoned when needed, silent when not, reading
before writing, always yielding to a human, and always citing what it read.

## Why this exists

There is a common failure mode in disaster comms tooling: the vendor drops
in a chatbot, and the chatbot either interrupts everything (talks in every
thread, drowns out humans) or does nothing useful (only responds to a
narrow command set nobody remembers).

The AI teammate protocol is an attempt to describe how an AI can be a
useful team member instead — with the same social rules a human on the
roster follows.

## Design principles

1. **Teammate, not agent.** The AI does not take independent action on
   the world. It answers questions, surfaces context, and drafts things
   for humans to confirm. Every write action requires an explicit human
   approval step.
2. **Summoned, not persistent.** The AI does not respond unless it is
   explicitly addressed. Silence is the default.
3. **Read-first.** Before responding, the AI reads recent messages in
   the channel and any tenant-scoped data it has access to. It waits a
   short window to give humans a chance to answer first.
4. **Yielding.** Any human can tell the AI to stop, at any point, and
   it stops — mid-response if necessary.
5. **Transparent.** Every response cites its sources. Every action —
   including every access denial — is written to an audit log the
   tenant can inspect.
6. **Tenant-scoped, always.** The AI reads only data the summoning
   sender is authorized to see. There is no "AI reads across tenants"
   mode.

## Summoning triggers

The AI teammate responds only when one of the following triggers is
present in an inbound canonical message's `body.text`:

| Trigger | Example |
|---|---|
| `@ai` (case-insensitive) anywhere in the message | `@ai what's the status of shelter 3?` |
| `@<name>` where `<name>` is a configured AI teammate name | `@iris show me all NEEDS from Abaco today` |
| `<name>` at the start of a line, followed by non-word char | `Iris, can you summarize the last hour?` |
| `<name>?` anywhere in the message | `Iris? what have we heard from Marsh Harbour?` |

If none of the above triggers is present, the AI teammate MUST NOT
respond, even if the message looks like it invites a response. Silence
is the default.

Configurable per deployment:

- The set of names the AI teammate answers to.
- Whether unqualified name mentions at the start of a line also count
  (some tenants want this off, to avoid triggering on `Iris said …`).
- Case sensitivity.

## The yield window

When a summoning trigger fires, the AI teammate does not respond
immediately. It waits a **yield window** to give humans a chance to
answer first. If a human answers the same question during the window,
the AI stays silent. If a human adds new context (a clarification, a
correction), the AI incorporates it into the response.

Recommended defaults:

| Message priority | Yield window |
|---|---|
| P1 / urgent (message contains `SOS` command; or channel policy tags it) | 3–5 seconds |
| Normal | 8–12 seconds |

Implementations MAY make the yield window configurable per deployment
and per channel. Longer than 12 seconds tends to feel unresponsive;
shorter than 3 seconds tends to interrupt humans in the middle of typing.

## Yielding on demand

At any point, any participant in the channel can tell the AI to stop:

| Instruction | Effect |
|---|---|
| `@ai stand down` | Cancel any in-flight response. Do not respond further in this thread until re-summoned. |
| `@ai hold` | Pause. Wait for `@ai continue` or `@ai stand down`. |
| `@ai continue` | Resume from a `hold`. |

The AI MUST honor these instructions from any participant with
send-rights on the channel, not only from the original summoner. A
disaster operator needs to be able to shut up an AI that is making
things worse, even if someone else called it in.

An in-flight response that is cancelled MUST NOT be delivered. If part
of it has already been sent (e.g. the first chunk of a streamed
message), the AI SHOULD send a short `[cancelled]` marker and stop.

## Read-only tool interface

An AI teammate accesses data via **tools**. A tool is a typed,
named, read-only capability the AI can call. Each tool declares:

```
Tool {
  name:          string    // stable, snake_case
  description:   string    // one-line, human-readable
  input_schema:  JSON Schema
  output_schema: JSON Schema
  scope:         string    // permission scope required to call this tool
  side_effects:  "none"    // v1: tools MUST be side-effect-free
}
```

V1 tools MUST be read-only (`side_effects = "none"`). Write-scoped tools
are on the roadmap and will require explicit human-in-the-loop
confirmation. There is no "the AI just did a thing" story in v1 —
because in a disaster, "the AI just did a thing" is a category of
incident nobody has bandwidth to handle.

### Typical read-only tools

Naming is illustrative — the shapes are what matters.

- `list_recent_messages(channel_id, since, limit)`
- `resolve_short_id(short_id)`
- `list_active_incidents(area?)`
- `get_shelter_status(shelter_id)`
- `list_pending_needs(area?)`
- `list_pending_missing_persons(area?)`

## Authorization model

The AI's read scope is derived from the **summoner's identity**, not
from a global "AI can read everything" grant.

```
inbound message
   → sender identity (from sender.endpoint + platform)
   → resolved account
   → tenant + roles + area scopes
   → AI inherits exactly those read permissions for this response
```

The AI MUST NOT read data the summoner would be denied. When the AI
attempts a read that is denied, it MUST:

1. Log the denial to the audit stream, with the tool call, the scope
   that was required, and the scope the summoner has.
2. Tell the summoner, plainly, that the read was denied and by what
   rule.

## Source citation

Every AI response MUST cite:

1. **The tools it called** and the arguments it passed.
2. **The messages it read** from the channel during the yield window.
3. **Any human message it is deferring to** ("Alice already answered
   this at 14:32, standing down") when it yields silently.

Citations are for the humans reading the response, not just for the
audit log. In practice this means each factual claim in the response
carries a short inline reference — `(from tool: list_pending_needs)`
or `(from message: Alice, 14:32)` — and a compact list of what was
read appears at the end.

Uncited claims are a spec violation. If the AI does not have a source,
it says so, or it does not make the claim.

## Human authority always wins

- A human message contradicting the AI is authoritative.
- A human `@ai stand down` immediately silences the AI.
- A human editing a factual claim in an AI response invalidates that
  response for downstream automation (nothing should re-trigger off
  the AI text once a human has corrected it).

The AI SHOULD periodically ask: *"do humans in this channel have
enough information now?"* and, if yes, stop generating.

## Audit

Every AI teammate deployment MUST emit an audit event for:

- Every summoning that fires.
- Every summoning that is suppressed because it yielded to a human.
- Every tool call, with arguments and outcome.
- Every access denial, with the missing permission.
- Every response delivered, with its full text and citations.
- Every `stand down` / `hold` / `continue` instruction received.

Audit events MUST be tenant-scoped and MUST be retrievable by the
tenant on demand. This is non-negotiable: an AI that acts inside a
disaster response without an audit trail is not acceptable.

Suggested audit event shape (illustrative, not normative):

```json
{
  "event_id": "01J...ULID",
  "ts": "2026-08-09T14:32:11.482Z",
  "tenant_id": "acct_nema_bahamas_ops",
  "channel_id": "chan_ops_main",
  "kind": "tool_call | tool_denied | response_sent | response_suppressed | instruction_received",
  "summoner": { "endpoint": "+12421234567", "platform": "whatsapp" },
  "payload": { ... kind-specific ... }
}
```

## Compliance checklist for an AI teammate implementation

- [ ] Silent by default; responds only on a configured summoning trigger.
- [ ] Yield window observed (P1: 3–5s, normal: 8–12s).
- [ ] Yields to human answers within the window.
- [ ] Honors `stand down` / `hold` / `continue` from any channel participant.
- [ ] Every tool used is declared with name, input/output schema, scope,
      and `side_effects = "none"` (v1).
- [ ] Read scope derived from summoner identity, never global.
- [ ] Every response cites tools called and messages read.
- [ ] Access denials are logged and surfaced to the summoner.
- [ ] Every action and denial written to a tenant-scoped audit stream.
- [ ] No write actions without a separate, explicit human confirmation
      turn (v1: no write actions at all).

## Non-goals

- **Agentic autonomy.** The AI teammate does not plan multi-step
  actions in the world. If a disaster response needs autonomous
  action, that is a human decision.
- **Persistent presence.** The AI is not "always on" in the channel.
  It does not summarize on a schedule unless summoned to.
- **Cross-tenant reasoning.** An AI instance sees one tenant's data at
  a time. Cross-tenant coordination is out of scope for this spec.

## Open questions (feedback wanted)

This is a draft. Specific points where outside review would help:

1. **Summoning by voice.** How does the summoning-trigger rule extend
   to a voice channel where there is no `body.text` to grep?
2. **Confidential channels.** Should there be a "no AI ever, even if
   summoned" flag at the channel level?
3. **Cross-language triggers.** How should the summoning name pattern
   handle Haitian Creole, Spanish, French, Dutch — the languages of
   the Caribbean beyond English?
4. **Model choice disclosure.** Does the audit event need to record
   *which* model produced a response (for reproducibility during a
   post-incident review)?

Please open a Spec Change Proposal issue if you have thoughts on any
of these before the v1 stable ratification.
