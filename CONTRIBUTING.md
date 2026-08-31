# Contributing to the FieldWatch Comms OpenKit

Thanks for considering a contribution. This project exists so that any
climate-vulnerable jurisdiction can stand up disaster-response comms
infrastructure without licensing a commercial product. Anything you contribute
becomes part of that public commons.

## Who we especially want to hear from

- Engineers, ops staff, and communications officers working inside Caribbean,
  Pacific, and other small-island-developing-state disaster-management
  agencies (NEMAs and equivalents).
- Responders — Red Cross, mutual-aid networks, search-and-rescue teams,
  radio operators — who have run coordination during a real event and can
  tell us where the spec falls short of what actually happens on the ground.
- Developers building humanitarian comms tools who want a common protocol to
  interoperate against.
- Standards, security, and privacy reviewers.

If you fall into any of these groups and have never contributed to an
open-source project before, please open an issue and say so. We will help.

## How to contribute

### Small changes (typos, clarifications, small bugs)

Open a pull request directly against `main`. No prior issue needed. Please:

1. Keep the change focused on one thing.
2. Sign off your commit (`git commit -s`) — it certifies that you have the
   right to submit the contribution under the project's license (this is the
   Developer Certificate of Origin at <https://developercertificate.org/>).
3. Describe *why* in the PR body, not just what.

### Larger changes (new features, new adapter reference impls, refactors)

Please open an issue first and briefly describe what you have in mind. This
is not a gate — it is so we can flag if someone else is already working on
the same thing, or if there is a design constraint you should know about
before spending your time. We will respond as fast as we can.

### Specification changes (schema, parser grammar, AI-teammate protocol, transport contract)

Specification changes need deliberation. Please open a **Spec Change
Proposal** issue (there is a template for it) rather than a pull request.
Anything in `spec/` is a protocol that adopters are expected to implement,
so a breaking change ripples outward and needs discussion.

### Big picture / product direction

Open an issue with the `discussion` label, or email `command@fieldwatchai.io`
if you would rather start in private.

## Style

- **Prose:** plain English. Assume the reader is a smart person who does not
  live in this codebase. Avoid jargon. If you need a term of art, define it
  the first time it appears.
- **Code (when we start accepting it):** each language's community standard
  is fine. For Go we follow `gofmt` and `go vet`. Prefer clarity over
  cleverness.
- **Commit messages:** conventional-commit style is preferred but not
  required. `docs:`, `spec:`, `fix:`, `chore:`, `examples:` are the ones we
  use most.

## Licensing

By contributing, you agree that your contribution is licensed under the
Apache License 2.0 (the project license — see [`LICENSE`](./LICENSE)).

## Conduct

See [`CODE_OF_CONDUCT.md`](./CODE_OF_CONDUCT.md). Enforcement contact is
`conduct@fieldwatchai.io`.

## Security

Do not report security issues in public PRs or issues. See
[`SECURITY.md`](./SECURITY.md) for the private disclosure address.
