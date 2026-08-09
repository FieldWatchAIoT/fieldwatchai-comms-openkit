# FieldWatch Comms OpenKit

**An open protocol and reference implementations for disaster-response communications infrastructure.**

Built in the Bahamas by [FieldWatch AI](https://fieldwatchai.io). Licensed Apache 2.0. Being prepared as a Digital Public Good.

---

## What this is

The Comms OpenKit is:

1. **A specification** — the message schema, command grammar, transport adapter contract, and AI-teammate protocol that FieldWatch's own comms hub runs on.
2. **A set of reference implementations** — starter code showing how to plug WhatsApp, SMS, Telegram, email, and (roadmap) satellite messengers into a disaster-response comms stack.
3. **A deployment recipe** — how any disaster-management agency in a small island developing state (SIDS) can stand up the same coordination surface FieldWatch runs, on their own infrastructure, without licensing a commercial product.

Any group — a National Emergency Management Agency (NEMA), a Red Cross chapter, a mutual-aid network, a private search-and-rescue team — can implement the spec and interoperate with the reference stack.

## Why this exists

In September 2019, Hurricane Dorian sat over the northern Bahamas as a Category 5 storm for roughly 40 hours. 74 people were confirmed dead. Around 245 people are still officially missing, seven years on.

The failure was not a lack of goodwill. Responders showed up from all over the world. The failure was coordination. Radio, WhatsApp, SMS, email, Facebook posts, family group chats, and paper lists were all being used simultaneously by people who could not see each other's traffic. Messages were duplicated, contradicted, or lost. Missing-person reports arrived days after they might have helped.

Commercial disaster-comms platforms exist. They cost hundreds of thousands of US dollars per year, per agency. No Caribbean government can license one for every department that needs it, and no small NGO can license one at all. The gap gets papered over with consumer messaging apps that were never designed for life-safety traffic and that share no common schema.

The Comms OpenKit is an attempt to close that gap with open protocol and open code that a country the size of the Bahamas — or Dominica, or Vanuatu, or any comparable jurisdiction — can adopt and run itself.

## Who this is for

- **Caribbean NEMAs and equivalent agencies** in small island developing states.
- **Governments and civil-defense bodies** in climate-vulnerable jurisdictions that need coordination infrastructure they can own outright.
- **Disaster-response NGOs** — Red Cross societies, mutual-aid networks, search-and-rescue teams — that need to interoperate across radios, WhatsApp, SMS, Telegram, email, and satellite messengers without a proprietary hub.
- **Developers** building communications tools for climate-vulnerable communities who want a reference protocol to interoperate against.
- **Researchers and standards bodies** working on humanitarian communications interoperability.

## What is in the box

| Component | Status | Location |
|---|---|---|
| Apache 2.0 license | shipped | [`LICENSE`](./LICENSE) |
| Canonical message schema (JSON Schema draft-2020-12) | shipped | [`spec/canonical-message.schema.json`](./spec/canonical-message.schema.json) |
| Canonical message spec (human-readable) | shipped | [`spec/canonical-message.md`](./spec/canonical-message.md) |
| Rule-based command parser grammar | shipped | [`spec/parser-grammar.md`](./spec/parser-grammar.md) |
| AI-teammate protocol (v1 draft) | shipped | [`spec/ai-teammate-protocol.md`](./spec/ai-teammate-protocol.md) |
| Transport adapter contract | shipped | [`spec/transport-adapter.md`](./spec/transport-adapter.md) |
| Digital Public Goods alignment mapping | shipped | [`docs/dpg-alignment.md`](./docs/dpg-alignment.md) |
| DPG manifest | shipped | [`dpg-manifest.yml`](./dpg-manifest.yml) |
| Deployment guide (docker-compose, ECS, k8s) | placeholder — targeting Q4 2026 | [`docs/deployment.md`](./docs/deployment.md) |
| Reference adapter: UltraMSG WhatsApp | scaffold — code Q4 2026 | [`examples/whatsapp-ultramsg/`](./examples/whatsapp-ultramsg/) |
| Reference adapter: Twilio SMS + WhatsApp | scaffold — code Q4 2026 | [`examples/twilio-sms/`](./examples/twilio-sms/) |
| Reference adapter: Telegram Bot API | scaffold — code Q4 2026 | [`examples/telegram-bot/`](./examples/telegram-bot/) |
| Reference adapter: AWS SES email | scaffold — code Q4 2026 | [`examples/aws-ses-email/`](./examples/aws-ses-email/) |
| Satellite adapters (ZOLEO, inReach, SPOT, Iridium SBD) | on the roadmap | — |

## Digital Public Goods alignment

The Digital Public Goods Alliance defines a DPG as open-source software, data, AI models, standards, or content that helps achieve the Sustainable Development Goals. This project targets:

- **SDG 11** — Sustainable Cities and Communities (target 11.5: reduce deaths and losses from disasters)
- **SDG 13** — Climate Action (target 13.1: strengthen resilience and adaptive capacity)
- **SDG 17** — Partnerships (target 17.6: technology cooperation)

A full mapping against the 9 DPG Standard indicators lives in [`docs/dpg-alignment.md`](./docs/dpg-alignment.md). Registry submission is targeted for Q1 2027.

## Roadmap

- **Q3 2026 (this quarter)** — publish the specs: canonical message, parser grammar, AI-teammate protocol, transport adapter contract.
- **Q4 2026** — publish reference transport adapters (Go) for UltraMSG, Twilio, Telegram, AWS SES. Publish `docker-compose` deployment recipe.
- **Q1 2027** — submit to the Digital Public Goods Alliance registry. Publish AWS ECS and Kubernetes deployment recipes. First satellite messenger adapter (ZOLEO).
- **Q2 2027** — voice channel spec (Vapi / ElevenLabs pattern). First adopter deployment outside FieldWatch.

Dates are targets. If a small team of Bahamian engineers can't credibly hit a date, we will move the date rather than ship something half-built. This project has to work when a storm actually lands.

## Status

**Early public release.** The specifications in `spec/` are the ones FieldWatch's own comms hub implements today, so they are already load-bearing internally. Reference implementation code is still being extracted from FieldWatch's private repositories and cleaned up for public release. Expect the roadmap dates above to firm up as we go.

The best way to influence the direction is to open an issue with your use case — especially if you work in a Caribbean or Pacific SIDS disaster-management context, or a comparable resource-constrained jurisdiction.

## Contributing

See [`CONTRIBUTING.md`](./CONTRIBUTING.md). Contributions from Caribbean and Pacific SIDS engineers are especially welcome — this is your infrastructure as much as anyone's.

Security issues: please do not open a public issue. See [`SECURITY.md`](./SECURITY.md).

Conduct: see [`CODE_OF_CONDUCT.md`](./CODE_OF_CONDUCT.md).

## License

Apache License 2.0. See [`LICENSE`](./LICENSE).

## Attribution and origin

The Comms OpenKit is developed and maintained by [FieldWatch AI](https://fieldwatchai.io), a company based in Nassau, The Bahamas, and founded after Hurricane Dorian.

This work is being carried out in part under a commitment to the UNFCCC AI for Climate Action Award programme. The award recognizes the intent to open-source the protocol and reference implementations underlying FieldWatch's disaster-response coordination product, so that any climate-vulnerable jurisdiction can adopt the same infrastructure without paying for it.

If your agency, NGO, or research group deploys this stack, we would love to hear about it — even a one-line email to `hello@fieldwatchai.io` helps us make the case that open protocols for humanitarian comms are worth investing in.
