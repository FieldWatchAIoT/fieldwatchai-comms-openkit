# Harms analysis

How this software could hurt people, what is built to prevent that, and what is
left to the deploying agency.

This is the do-no-harm assessment for the Digital Public Goods Standard
(indicator 9). It is deliberately specific. A generic statement that we care
about safety would be worth nothing to an agency deciding whether to route
life-safety traffic through this.

Scope: the [`spec/`](../spec/) and the reference implementations in
[`implementations/`](../implementations/). Data handling is covered separately
in [`privacy.md`](./privacy.md).

---

## The context that shapes everything

This software carries messages during disasters. That means:

- **The traffic is life-safety.** A message may be the only record that someone
  is trapped. Losing it, misrouting it, or acting on a wrong interpretation has
  physical consequences.
- **The senders are under duress.** People text badly when frightened. The
  system must degrade toward asking, never toward guessing.
- **The operators are overloaded.** During an event, staff are exhausted and
  triaging faster than they can think. Anything ambiguous will be misread.
- **The window is short.** A failure that takes a day to notice has already cost
  what it was going to cost.

Every design decision below follows from those four facts.

---

## Harm 1 — Acting on a message the system misunderstood

**The risk.** Someone texts `42 STATUS full`. The system reads the wrong shelter
and reports it full. Resources are redirected. The shelter that is actually full
receives nothing.

**What is built:**

- **The parser is rule-based and deterministic.** No language model sits on the
  hot path deciding what a message means. The same message always resolves the
  same way — no temperature, no drift between versions, no improvisation under
  an unusual phrasing.
- **Confidence is scored, and low confidence does not proceed.** The policy gate
  has three outcomes: act, confirm, or ask. Only a high-confidence match
  executes.
- **Ambiguity produces a question, not a guess.** On a partial match the system
  echoes back what it understood and waits. A wrong guess sends a boat to the
  wrong island; asking costs one message.
- **`OOPS` recalls a message** within a per-channel window, so a sender who sees
  a wrong echo can cancel before anything acts on it.

**What remains with you.** The thresholds are yours to set. Raising them toward
1.0 makes the system ask more and act less — usually the right trade in a
life-safety context. Test them against your own short-id scheme before an event,
not during one.

## Harm 2 — A message arrives and is silently dropped

**The risk.** The most dangerous failure in this software is not a crash. It is
accepting a message, returning `200`, and doing nothing with it — because
nobody notices until someone asks why their report was ignored.

**What is built:**

- **`GET /v1/diagnostics`** names every configuration gap that causes silence,
  with the specific account or channel and the command to fix it.
- **The console** shows the same state on one page, so "is it receiving?" is
  answerable without database access.
- **Unroutable accounts are a `blocking` finding**, not a warning.
- **Setup wires the link by default**, because the historical failure was an
  account with no channel — which stores traffic and forwards nowhere.
- **The end-to-end CI job** asserts a real message reaches the database with the
  right policy action, so this failure mode cannot regress unnoticed.

**What remains with you.** Poll `/v1/diagnostics` from your monitoring. A
`healthy: false` should page someone. The software can detect the condition; it
cannot make anyone look.

## Harm 3 — Someone impersonates a shelter, a responder, or an agency

**The risk.** A false `STATUS` from a spoofed number sends people to a shelter
that is closed, or away from one that is open.

**What is built:**

- **Inbound verification is fail-closed** on every adapter — HMAC signature
  where the platform offers one (Twilio), constant-time shared-secret comparison
  where it does not (UltraMSG, Telegram, SES), plus a topic-ARN allowlist for
  SES. An unverifiable request is rejected, never accepted with a warning.
- **Unregistered senders cannot route.** A message on an account you have not
  registered is acknowledged and dropped.

**What remains with you — and this is the significant one.** Verification proves
the message came from the *platform*, not that the human is who they claim.
WhatsApp and SMS identity is a phone number, and phone numbers are transferable,
spoofable on some routes, and taken from people along with their phones.

Do not treat an inbound endpoint as authentication for consequential action. If
a message can trigger something irreversible, put a human in the loop. The
`echo_back` path exists partly for this.

## Harm 4 — Harassment, flooding, and abuse of the channel

**The risk.** Someone floods the number with false `SOS` messages, drowning real
ones. Or uses it to harass an operator. Or discovers that a `MISSING` report
publishes a person's name somewhere.

**What is built:**

- **Body caps** (`MAX_BODY_BYTES`, 256 KB) bound a single request.
- **Idempotency** on `(account_id, platform_message_id)` means a replayed
  message is stored once, so retry storms cannot inflate the record.
- **Deliberate drops return `200`**, which discourages upstream retry storms
  that would degrade the platform during an event.
- **Nothing is published anywhere.** The software has no public surface. A
  `MISSING` report goes to your database and your consumer, not to the internet.

**What remains with you, honestly:**

- **There is no rate limiting per sender.** A determined flooder can fill your
  message table. Put rate limiting at your edge (ALB, Cloudflare, API gateway).
- **Suspending an account does stop it**, and is the in-app lever:
  `PATCH /v1/accounts/{id}` with `status: "suspended"`. A suspended account
  stops resolving, so inbound messages are acknowledged and dropped. Note it is
  **not instantaneous** — the webhook caches a successful account lookup for 60
  seconds, so allow up to a minute for suspension to take effect. For an active
  attack, block at the platform or the edge as well.
- **There is no blocklist.** Blocking a specific sender is not implemented.
- **There is no content moderation.** Nothing classifies or filters message text.
  Assume any body may contain abuse, and never render it as HTML without
  escaping.

These are real gaps. They are stated rather than papered over because an agency
planning a deployment needs to know what it must add.

## Harm 5 — An AI participant doing something it should not

**The risk.** An AI teammate misreads a situation and takes an action, or speaks
with authority it has not earned, and a human defers to it during an event.

**What is built** — the [AI-teammate protocol](../spec/ai-teammate-protocol.md)
constrains any AI participant to be:

- **Silent by default**, and **summoned only**. It does not volunteer.
- **Read-only in v1.** No autonomous world-changing action during a response.
- **Always yielding to humans**, and silenceable by any participant at any time.
- **Audit-logged** on every action.

And structurally: **no model sits on the interpretation path.** Parsing is
rule-based. An AI participant is a participant, not the router.

**What remains with you.** The protocol is a v1 draft published specifically to
attract outside review before it stabilises. If you are deploying AI
participation, read it critically and tell us what is wrong with it.

## Harm 6 — The data itself becomes the harm

**The risk.** A disaster message log is a map of who was where, who was hurt, and
who was alone. In the wrong hands — an abusive partner, a hostile authority, a
breach — that is dangerous well after the storm.

**What is built:**

- Per-account platform credentials **encrypted at rest** (AES-256-GCM or KMS).
- **Media is never re-hosted** — attachments stay as links to platform storage.
- **Raw payloads excluded** from the console, the message API, and exports by
  default.
- **Erasure and retention purge**, with dry-run defaults.
- **No telemetry.** Nothing leaves your infrastructure.

**What remains with you.** `INTERNAL_API_TOKEN` is a shared admin credential:
anyone holding it can read every tenant's traffic. Per-actor identity is a future
revision. Treat that token as a root password. Set a retention window and
actually run the purge — data you deleted cannot be breached. See
[`privacy.md`](./privacy.md).

## Harm 7 — Dependence on something that fails when needed

**The risk.** An agency builds its coordination around this, and it is
unavailable during the event it was adopted for.

**What is built:**

- **The console loads no external resources** — no CDN, no webfont, no
  framework. A page that fetches from the internet is broken exactly when the
  uplink is what failed. A test enforces this.
- **Durable queueing** between acceptance and processing, so a downstream outage
  delays rather than loses.
- **Replay** (`POST /v1/workflows/replay`) re-fires forwards a consumer missed.
- **Fail-closed boot.** The service exits rather than serving traffic it cannot
  persist — with a restart policy, so it recovers when the database returns.

**What remains with you.** This is one channel among several. **Do not retire
your radio.** Satellite adapters — for when the towers are down — are on the
roadmap and not in the box.

---

## What we will not build

Stated so adopters know the boundary and can plan around it:

- **Covert or silent collection.** Nothing that gathers messages without the
  sender knowing they contacted an agency.
- **Location tracking beyond what a sender attached.** No background location,
  no inference from message content.
- **Profiling or scoring of individuals.** The system scores its own confidence
  in a parse — never a person's credibility.
- **Autonomous AI action on life-safety traffic.** Read-only stays.
- **Public-facing publication of message content.** Nothing here becomes a
  public feed.

A contribution implementing any of these will be declined regardless of quality.

---

## Known gaps

Collected in one place so nobody has to read the whole document to find them:

| Gap | Consequence | Mitigation available today |
|---|---|---|
| No per-sender rate limiting | A flooder can fill the message table | Rate limit at the edge; suspend the account |
| No sender blocklist | An abusive sender cannot be blocked in-app | Suspend the account, or block at the platform |
| No content moderation | Message bodies may contain abuse | Escape on render; human review |
| Shared admin token, no per-actor identity | Anyone with the token reads every tenant | Treat as a root password; rotate |
| `sender_contact_id` not populated | Contact erasure only reaches registered endpoints | Erase by endpoint; verify with a dry run |
| No satellite channel | Nothing works when towers are down | Keep radio; satellite is on the roadmap |
| Message bodies not encrypted at column level | Relies on database encryption at rest | Enable your database's encryption |

---

## Tell us what we have missed

This analysis was written by the people who built the software, which is exactly
the wrong vantage point for finding its blind spots.

If you work in disaster response, digital safety, or humanitarian protection and
you can see a harm we have not listed, please open an issue — or email
`security@fieldwatchai.io` if raising it publicly would itself create risk.

Contributions from people who have coordinated an actual response are worth more
here than any amount of review from engineers.
