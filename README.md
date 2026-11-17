<p align="center">
  <img src="site/public/mark.svg" width="72" alt="Custodia logo">
</p>

<h1 align="center">Custodia</h1>

<p align="center">
  <strong>The inventory of your security tools, with their metrics.</strong><br>
  Custodia connects the tools you already run, collects their inventory with provenance and publishes metrics declared by each connector. Every value carries its source, date and whether the run was complete or partial.
</p>

<p align="center">
  <a href="https://custodia-demo.pages.dev/?lang=en">Live demo</a> ·
  <a href="https://custodia.octaviosantacruz.com">Website</a> ·
  <a href="#how-it-works">How it works</a> ·
  <a href="#status-and-roadmap">Roadmap</a> ·
  <a href="docs/PRODUCT.md">Product</a> ·
  <a href="LICENSE">Apache 2.0</a>
</p>

<p align="center"><strong>English</strong> · <a href="README.es.md">Español</a></p>

> **Prototype:** the interface is complete and runs as a demo with fictional data. The Go backend and real adapters are in progress.

![Custodia: one person crossed across CyberArk and Netskope, with the verdict and its evidence](site/public/shots/en/person.png)

*The demo running on a fictional company: how one person is joined in each tool, and the verdict with its evidence.*

## The problem

Every security tool has its own console and its own way of counting. The PAM
knows how many Safes and accounts it manages; the SIEM knows how many agents
report; the rest lives in spreadsheets that age the same day. Comparing tools
means opening one console at a time and trusting numbers with no date and no
provenance.

Custodia connects those tools, keeps the inventory with its origin and
publishes metrics declared by each connector. A value without data is
"unavailable", never zero; a partial run is marked and is not used for
conclusions that need the total.

## What it answers

| Question | What Custodia shows |
|---|---|
| Which tools are connected and when did they run? | Inventory per connection, collections, last publication and completeness. |
| How many agents, Safes or accounts are there? | Totals and breakdowns by state, with the data date. |
| What changed or stopped reporting? | Age, partiality and unavailable values, never an invented zero. |
| Where does each number come from? | Drill-down to the exact records that compose the metric and its run. |

## How it works

1. **Connect.** An administrator loads the tool from the console with URL,
   encrypted credentials or a reference, CA and collections. A probe validates
   access before scheduling.
2. **Collect.** A worker reads only the declared collections, keeps the
   external ID, run and age, and publishes transactionally.
3. **Measure.** Each connector declares its metrics in the binary; they are
   computed on read over the last publication. No data means "unavailable",
   not zero.

## Design principles

- **Provenance on everything.** Each fact names its source, external ID,
  date and run.
- **Unknown is not a gap.** A partial or failed capture yields "unknown",
  never a false alarm. Absence, missing permission and zero observed are
  different states.
- **No silent guesses.** Similar names never merge two records into one
  identity.
- **Humans decide, syncs don't overwrite.** Observations from tools and
  decisions from people are separate layers.
- **Read-only by design.** Custodia queries your tools; it does not change
  them.

## Who it is for

Security and infrastructure operators who juggle several consoles and keep
answering the same inventory questions by hand: access reviews, coverage
checks, offboarding, audits.

## Sources

| Implemented | Open-source catalog next |
|---|---|
| CyberArk PAM (Safes, members, accounts, platforms, PSM) · Wazuh (agents, groups) | Fleet, DefectDojo, CrowdSec, OPNsense, Keycloak, MISP, OpenBao; commercial tools need a customer tenant |

Each new source arrives with its contract, allowlist and metrics. There is no
"Identity population" in the MVP: the focus is inventory and metrics per tool.

## Status and roadmap

Custodia is a self-hosted MVP in progress. Local login, OIDC, inventory with
metrics, Wazuh accepted against 4.14.10, encrypted secrets and the TLS
installer are implemented and tested in the laboratory. Console updates and
the production build cut close this batch; acceptance on a real host is still
pending.

| Stage | What | Status |
|---|---|---|
| Access | Local login, sessions, CSRF, administration and configurable OIDC. | Done in lab |
| Inventory | Declared metrics, CyberArk PAM and Wazuh with their own acceptance. | Done |
| Operations | Installer, TLS, backups, signed update and rollback. | Local gate done |
| Distribution | Public release, disposable host and third-party notices. | Pending |

The demo keeps the DS Hub legacy modules (People, Netskope SSE, SIA/Risk,
DSPM); the production build excludes them.

## Try it

The fastest way is the [live demo](https://custodia-demo.pages.dev/?lang=en):
no sign-up, nothing leaves your browser. To run it locally:

```bash
cd web
npm ci
npm run preview:demo   # http://127.0.0.1:4184
```

Switch language from the user menu or with `?lang=en` / `?lang=es`.

## Repository

| Path | What |
|---|---|
| `web/` | The app: React, Vite and TypeScript. Builds against a real API or as a static demo with a simulated API ([WEB.md](docs/WEB.md)). |
| `site/` | The product site (`npm run build:site`). |
| `docs/` | Product, architecture, connectors, design and decisions (in Spanish). |
| `demo/` | An earlier, reduced experiment; not the current demo. |

## Development

```bash
npm run verify:web      # types, lint, styles, unit tests, demo build and e2e for web/
npm run build:site      # product site
npm run verify          # docs + company and experiment models
```

Requires Node.js 24 or later. Browser tests use Playwright with Chromium
(`npx playwright install chromium`).

The interface is written in Spanish and translated to English: texts are
wrapped as `t('Texto en español')` and English lives in
`web/src/cross/i18n/en.ts`. `node scripts/i18n-report.mjs --missing` (in
`web/`) lists anything untranslated.

## Documentation

The project documentation is written in Spanish.

| Topic | Document |
|---|---|
| Scope and stages | [Product](docs/PRODUCT.md) |
| Fictional company, site and demo | [WEB.md](docs/WEB.md) · [COMPANY-DEMO.md](docs/COMPANY-DEMO.md) |
| Go and SQLite | [Architecture](docs/ARCHITECTURE.md) |
| Configuring and syncing tools | [Connectors](docs/CONNECTORS.md) |
| Sign-in, OIDC and QA | [Access](docs/ACCESS.md) |
| Interface and sample walkthrough | [Design](docs/DESIGN.md) |
| Criteria to move forward | [Validation](docs/VALIDATION.md) |
| Where the demo content comes from | [Provenance](docs/PROVENANCE.md) |
| Screenshots and ES/EN copy | [Presentation](docs/PRESENTATION.md) |
| Accepted decisions | [Decisions](docs/DECISIONS.md) |
| Previous version, what was reused and retired | [Reference](docs/REFERENCE.md) |
| Pending work and delivered evidence | [Pending](docs/TO_IMPLEMENT.md) · [Closed](docs/IMPLEMENTED.md) |

## License and trademarks

Custodia started as an internal tool (DS Hub) and is now open source under
the [Apache 2.0](LICENSE) license. CyberArk, Netskope and the other product
names are trademarks of their owners and identify the tools Custodia reads
from; Custodia is not affiliated with them.

## Go backend laboratory

The first self-hosted installation targets single-instance SQLite on local disk.
The [Go laboratory](server/README.md) implements synthetic people/coverage reads
and bounded read-only HTTP inventory sync for normalized users/devices,
CyberArk PAM Safes, Safe members, non-secret account metadata, the platform
catalog, the PSM server/connector catalogs and explainable platform↔PSM links
against local fixtures, with a background worker and per-connection schedule;
real authentication, provider credentials and a distributable application
remain pending. Run `npm run verify:go` for the backend checks.
`npm run verify:go:ops` exercises online SQLite backup, restore on a disposable
instance and the self-hosted package manifest; `npm run package:go` builds the
binary, web assets and SHA-256 manifest without publishing.

The explicit `serve` mode now verifies per-request Ozal identity assertions
and supports host-side bootstrap of the first administrator.
`npm run verify:go:gateway` checks signed entry, scoped access and logout with
the real UI and a disposable gateway fixture. Acceptance against a deployed
Ozal instance remains pending; see [access](docs/ACCESS.md).
