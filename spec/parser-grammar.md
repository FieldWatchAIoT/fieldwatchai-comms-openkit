# Rule-based Command Parser Grammar

**Version:** 1.0
**Status:** stable
**Applies to:** `body.text` of a canonical message ([`canonical-message.md`](./canonical-message.md))

The parser extracts structure from an inbound message body so that routing,
policy, and consumer workflows can act on it. It is a **pure function** — no
database, no AI, no side effects. It answers "what did this message try to
say?" and nothing else.

Whether an extracted `short_id` actually resolves to a known contact or
group is the resolver's job, not the parser's. The parser is deliberately
permissive about the short-id token.

## Why rule-based (and not just an LLM)

- **Determinism.** During an incident, operators need to be able to predict
  what a message will do. `42 STATUS all clear` must parse the same way
  every time, regardless of load, model version, or context window.
- **Cost.** A parser that runs on every inbound message must be cheap.
- **Failure mode.** When the parser cannot make sense of a message, that
  is a first-class signal ("route to human triage"), not a soft failure.
- **Audit.** A rule-based parser can be reasoned about, code-reviewed, and
  tested exhaustively.

An LLM-based interpreter can sit *downstream* of this parser (see
[`ai-teammate-protocol.md`](./ai-teammate-protocol.md)), summoned
explicitly, for messages that need judgement rather than structure.

## Grammar (BNF-style)

```
MESSAGE   ::= SHORT_ID ( WS TARGET_CLAUSE )? ( WS REST )?
SHORT_ID  ::= ALNUM{1,8}         ; 1–8 alphanumerics, case-insensitive
ALNUM     ::= [A-Za-z0-9]

TARGET_CLAUSE ::= REDIRECT WS TARGET
REDIRECT      ::= "to" | "→"           ; case-insensitive "to"
TARGET        ::= "@" NAME
NAME          ::= [^\s]+                ; the token that follows "@"

REST      ::= COMMAND ( WS PAYLOAD )?   ; when no TARGET_CLAUSE was present
           | PAYLOAD                    ; when a TARGET_CLAUSE was present
COMMAND   ::= WORD                      ; upper-cased, matched to command set
PAYLOAD   ::= .+                        ; free-form remainder, trimmed

WORD      ::= [^\s]+
WS        ::= (\s)+
```

## Semantics

### The short id

- 1 to 8 alphanumeric characters. Case-insensitive.
- The parser does not verify that the short id resolves to a known
  contact / group. That is the resolver's job.
- Non-alphanumeric characters, or a token longer than 8, cause the parse
  to fail (`OK = false`).

### The target clause (optional)

If the second token is `to` (case-insensitive) or the arrow character
`→`, and the token after it starts with `@`, the parser treats the
message as **addressed to a target** and the remainder as free-text
payload — not as a command.

Rationale: a message like `42 → @abaco we have overflow capacity` is a
person-to-group forward, not a command invocation. Trying to interpret
`we` as a command produces noise.

If the arrow / `to` is present but not followed by an `@name`, the
parser falls through to normal command parsing (treats the `to` / `→`
as the command word, which usually results in `known_command = false`).

### The command (when no target clause)

- The first token after the short id.
- Upper-cased before matching.
- Checked against the channel's configured command set. If it matches
  case-insensitively, `known_command = true`; if not, `known_command =
  false` (the token is still returned, so operators can see what was
  attempted).

### The payload

- Everything after the command (or after the target, when a target is
  present), trimmed of surrounding whitespace.

### Failure

The parse returns `ok = false` when:

- The body is empty or all whitespace.
- The first token is not a valid short id (non-alphanumeric or > 8 chars).
- The body consists of only a bare short id with nothing following it
  (ambiguous — no command, no target). Downstream code should treat this
  as a request for help / a "did you mean" prompt.

## Result shape

```
ParseResult {
  ok:            bool     // false if the message is unroutable
  short_id:      string   // extracted short id, upper-cased (or lower — implementer's choice, but consistent)
  has_target:    bool
  target:        string   // the "@name" without the "@"
  command:       string   // upper-cased command token; empty when has_target = true
  known_command: bool     // true iff command matched the channel's command set
  payload:       string   // trimmed remainder
}
```

## Standard command set

Individual channels can configure their own command set. The following
commands are the **standard set** the reference implementations ship with;
adopters SHOULD implement them if the semantics apply, to keep operator
retraining costs down when moving between deployments.

| Command | Semantics | Typical payload |
|---|---|---|
| `STATUS` | Report status of a location, asset, or person. | free text describing the state |
| `NEEDS` | Request supplies, equipment, or personnel. | item + quantity |
| `DAMAGE` | Report damage to infrastructure / property. | free text; optionally a location share |
| `MISSING` | Report a missing person. | name + last known location |
| `FOUND` | Report a person previously reported missing has been located. | name + current location |
| `RESOURCE` | Offer a resource (shelter capacity, fuel, boat, generator). | resource + quantity |
| `HERE` | Report current location. | optional description; often paired with `body.location` |
| `NOTE` | Free-form note for the log, no expected action. | free text |
| `SOS` | Life-threatening emergency. | free text; treat as highest priority regardless of channel policy |

Reserved for future use: `ACK`, `RECALL`, `HELP`.

## Confidence scoring (recommended)

The parser itself returns booleans (`ok`, `known_command`) rather than a
continuous score. Downstream policy layers that want a confidence signal
can compute one from the parse result. A recommended rubric:

| Rating | Condition |
|---|---|
| **certain** | `ok = true` and `known_command = true` and the short id resolves to exactly one known contact / group |
| **probable** | `ok = true` and either the short id resolves fuzzily (edit distance 1) *or* the command is edit-distance-1 from a known command |
| **ambiguous** | `ok = true` and (the short id resolves to more than one candidate *or* the command is edit-distance-2 or worse from any known command *or* there is a `has_target` with no resolvable target) |
| **unparseable** | `ok = false` |

Implementations SHOULD use `strdist.LevenshteinDistance` or an equivalent
edit-distance implementation. Fuzzy matches SHOULD NOT auto-act — the
recommended pattern is to echo back what the parser thinks the operator
meant and wait for confirmation on ambiguous / probable messages.

## Worked examples

Using the standard command set.

| Input | Parses to |
|---|---|
| `42 STATUS all clear at shelter 3` | ok, short_id=`42`, command=`STATUS`, known=`true`, payload=`all clear at shelter 3` |
| `abc1 NEEDS water 40 gallons` | ok, short_id=`abc1`, command=`NEEDS`, known=`true`, payload=`water 40 gallons` |
| `42 → @abaco we have overflow capacity` | ok, short_id=`42`, has_target=`true`, target=`abaco`, payload=`we have overflow capacity`, command=`""` |
| `42 to @nema-ops shelter 3 at capacity` | ok, short_id=`42`, has_target=`true`, target=`nema-ops`, payload=`shelter 3 at capacity`, command=`""` |
| `42 SOS boat capsized 2 souls onboard` | ok, short_id=`42`, command=`SOS`, known=`true`, payload=`boat capsized 2 souls onboard` |
| `42 STATS shelter 3 ok` | ok, short_id=`42`, command=`STATS`, known=`false`, payload=`shelter 3 ok` (`STATS` is edit-distance-1 from `STATUS`; policy layer flags as *probable*) |
| `42` | not ok (bare short id, ambiguous) |
| `hello world` | not ok (`hello` is a valid short id but `world` is a payload with no command; some implementations return ok=false, others ok=true known=false — reference impl returns ok=true, unknown command) |
| `#42 STATUS ok` | not ok (short id contains non-alnum) |
| `verylongid STATUS ok` | not ok (short id > 8 chars) |
| `` (empty) | not ok |

## Conformance

An implementation is conforming if, given the same input text and the
same command set, it returns the same `ok`, `short_id`, `has_target`,
`target`, `command`, `known_command`, and `payload` values as this
specification.

Casing of the returned `command` field: implementations SHOULD upper-case.
Casing of `short_id` and `target`: implementations MAY preserve source
casing or canonicalize, but MUST be consistent within a deployment (the
resolver is doing case-insensitive lookup either way).
