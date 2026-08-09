# Security Policy

## Reporting a vulnerability

If you believe you have found a security vulnerability in the FieldWatch Comms
OpenKit — in the published specifications, in any reference implementation
code, or in an example configuration that would put an adopter at risk — please
report it privately.

**Do not open a public GitHub issue.** Public issues about security
vulnerabilities put every current adopter at risk before a fix can be
distributed.

### How to report

Email: **security@fieldwatchai.io**

Please include, to the extent you can:

- A description of the issue and the impact you think it could have.
- The specific file, spec section, or example that is affected.
- Steps to reproduce, if the issue is in code.
- Whether the issue is already public anywhere (a paper, a CVE, a talk).
- Whether you would like to be credited in the fix advisory, and if so, how.

If you prefer PGP, request our current key at the same address and we will
respond with it.

### What to expect

- **Acknowledgement within 3 business days** that we have received the report.
- **A first assessment within 10 business days** — whether we believe the
  issue is a vulnerability, its severity, and a rough remediation timeline.
- **Coordinated disclosure.** We will work with you on a disclosure date. We
  will not publish before we have a fix ready for adopters, and we will not
  ask you to sit on a disclosure indefinitely.
- **Credit** in the release notes / advisory, if you want it.

This project is early. Our response times will get faster as the community
around it grows. If you do not hear back within the windows above, feel free
to email again — it will not be taken as pressure, it will be taken as a
reminder that we owe you a response.

## Scope

In scope:

- The specifications published in this repository (`spec/`, `docs/`).
- Reference implementation code in this repository (`examples/`, when shipped).
- The DPG manifest and any published metadata (`dpg-manifest.yml`).

Out of scope for this repository (but please still tell us if you find
something — we will route it):

- FieldWatch's own hosted product at `fieldwatch.earth`.
- Third-party platforms the reference adapters integrate with (WhatsApp,
  Twilio, Telegram, AWS SES). Report those to the platform vendor.

## No bug bounty (yet)

We do not run a paid bug bounty program. When we do, we will announce it here.
Until then, all we can offer is credit, thanks, and — if you would like — an
introduction to whoever is doing similar work in humanitarian comms that we
know of.
