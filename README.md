# Fiken CLI

Agent-native Fiken tooling: complete read coverage over a local, searchable mirror — plus the gated, idempotent, audited write and reconciliation layer no other Fiken tool has.
Reads are a commodity. Twelve resources — contacts, journal entries, transactions, purchases, sales, invoices, credit notes, products, projects, accounts, bank accounts and the inbox — are mirrored into local SQLite so an agent can reason over your accounting history with SQL and full-text search instead of round-tripping a rate-limited API. The read layer is generated and disposable — re-syncable from the API at any time.

The write path is not disposable. It touches real books, so it is hand-built around a single rule: **an action against the ledger should be impossible to do twice, impossible to do unrecorded, and possible to undo.** Fiken gives you none of those — no idempotency key, no dry-run, no write audit. Three commands implement the rule today: `commit`, `reconcile` and `reverse`. The generated commands write straight to Fiken, so an agent driving them owns its own audit trail through `log-event`. See the roadmap for the gaps.

## Two layers

**Reads — generated, disposable.** ~150 Fiken v2 operations as composable, single-purpose commands, plus an MCP server for agents that prefer it. A local SQLite mirror with FTS answers history questions offline, so you don't hammer Fiken's single-concurrent-request limit to find out what a vendor cost last quarter. Delta-synced; rebuildable from the API at will.

**Writes & reconciliation — hand-built.** `commit`, `reconcile` and `reverse` are gated, date-aligned, and append to the audit log in the same call that makes the change. `commit` alone carries the idempotency map.

## Reconciliation & error-hunting

The surface no Fiken tool has, built around bankavstemming — the most-cited Fiken pain there is — all read-only and answered from the local mirror:

- **bank-unverified** — ledger-vs-reconciled-balance gaps and postings dated after the reconciled point: *which accounts have drifted and need a session.*
- **drift** — sub-krone øre-rounding drift, header-vs-lines totals and VAT-regime breaches, exact integer-øre.
- **vat-anomaly** — MVA codes implausible for the account, or off the vendor's usual pattern.
- **duplicates** — same vendor + øre amount within N days.
- **missing-bilag** — purchases and journal entries with no attached documentation, ranked by amount.

`bank-unverified` is the API-side health signal — it tells you where to look. The line-by-line matching itself runs through the browser **Superføring** workflow, which uses this CLI's idempotent `commit` and records into its audit log. The CLI owns the books-side correctness; the browser harness owns the bank-line UI.

## The gated write path

Writing to the ledger is a pipeline, not a call:

- **prepare** — an inbox document becomes a posting proposal: the original bilag, the vendor's posting history, candidate accounts, plausible MVA codes.
- **validate** — the dry-run Fiken lacks: balanced debit/credit, account exists, MVA code plausible, period open, date sane. Structural checks before anything touches the books.
- **commit** — idempotent (the same bank line or document never double-books), **date-aligned to the bank line** so the posting rendezvous with the waiting line in Fiken's own reconciliation, stamped with an `X-Request-ID`, and audited in the same call.
- **reverse** — a correcting entry or soft-delete with audit linkage. Mistakes are correctable, not catastrophic.
- **reconcile** — registers a payment against an open sale or purchase, audited. Clearing-account entries for payment-processor payouts are a roadmap item.

Plus **mva-summary**, **rollup**, and **vendor-profile** — VAT-return-shaped and margin/revenue/cost views read straight off the mirror. Because Fiken holds the books of record while ecommerce analytics see only a subset of revenue, these report the *whole-business* number, not the storefront's projection of it.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/nytafar/fiken-cli/main/install.sh | bash
npx skills add nytafar/fiken-cli --skill '*' --agent claude-code --global
```

The first line installs the latest release binary into `~/.local/bin` (checksum-verified). The second installs both agent skills, `fiken` and `fiken-superforing`, through [`npx skills`](https://skills.sh/). Add `--with-mcp` after `bash -s --` for the MCP server, `--from-source` to build from a clone. Details, other agents and Windows: [docs/install.md](docs/install.md).

Auth is a personal API token from Fiken (Settings -> API), exported as `FIKEN_API_TOKEN`; OAuth2 via `fiken-cli auth login` for acting on behalf of other companies. Every write is refused unless the company is a Fiken test company; real books need `FIKEN_MODE=live` in an untracked env file, never a flag. See [docs/configuration.md](docs/configuration.md).

## Quick start

```bash
# verify auth + connectivity (set FIKEN_API_TOKEN first, or run 'fiken-cli auth login' for OAuth2)
fiken-cli doctor

# find your companySlug — every analytical command is scoped to it
fiken-cli companies

# build the local mirror across your companies (serial + throttled; safe to re-run,
# resumes, and after the first run asks only for what changed per company)
fiken-cli sync

# refresh only what changed lately (contacts, journal_entries, transactions,
# products, sales, invoices, credit_notes; the rest still pull in full)
fiken-cli sync --since 7d

# the headline question: what isn't matched to the bank yet?
fiken-cli bank-unverified --company fiken-demo --as-of 2026-05-31

# find rounding-off or mis-coded entries before MVA (no --period = the current calendar year)
fiken-cli drift --company fiken-demo --period 2026-05

```

After upgrading past the mirror-key fix, refresh the two resources that were stored under stale keys, once per company:

```bash
fiken-cli sync --full --company <slug> --resources journal_entries,bank_accounts
```

`fiken-cli doctor` names any company still holding stale rows.

`--since` takes a duration (`7d`, `24h`, `1w`, `30m`) and asks Fiken for rows modified since
then, on the seven resources whose list endpoints accept a date filter: contacts,
journal_entries, transactions, products, sales, invoices and credit_notes. Every other
resource has no such filter and is fetched in full, with a `resource_not_incremental`
warning. `--full` and `--since` are refused together: one refetches every row, the other a
window.

A plain `fiken-cli sync` is incremental on its own on those same seven resources: each
company and resource keeps its own watermark, and the next run asks Fiken only for what
changed since that company's last **complete** pull of it — a pair that has never
completed one is fetched in full, which is what the first run does. The watermark is the
start of the run that filled it, moved back a day to cover the API's day-granular filter,
and it is written only after a pull that landed every page with no error and no row-count
mismatch, so an interrupted run re-pulls rather than skips. Each windowed pair emits
`{"event":"sync_window","company":...,"resource":...,"since":...}` in `--json` mode.
`--since` is a caller window and never touches the watermark; `--full` ignores it, clears
it for the pairs it refetches, and writes it again from the full pull. Two consequences
worth knowing: a windowed pull can never conclude that a missing row was deleted, so the
deletion sweep runs on a pair's first complete pull and on `--full`, not on the cheap
daily run; and an incremental run does not update the recorded `result_count`, because the
API's count then describes the window rather than the collection.

## Documentation

- [docs/features.md](docs/features.md): the detectors (`bank-unverified`, `drift`, `vat-anomaly`, `duplicates`, `missing-bilag`), the write pipeline (`prepare`, `validate`, `commit`, `reverse`, `reconcile`, `log-event`), analytics (`mva-summary`, `rollup`, `vendor-profile`), report shape, periods and recipes.
- [docs/commands.md](docs/commands.md): the generated command reference, output formats, agent flags and exit codes.
- [docs/configuration.md](docs/configuration.md): auth, the write guard and live mode, config file, environment, troubleshooting.
- [docs/install.md](docs/install.md): installer options, other agents, from source, developing the skills.
- [docs/mcp.md](docs/mcp.md): `fiken-mcp` for Claude Code and Claude Desktop, MCP over HTTP with Fiken OAuth; [docs/serving-publicly.md](docs/serving-publicly.md) for a public endpoint.
- [docs/design.md](docs/design.md): design principles and the roadmap of known gaps, so nothing above reads as a guarantee it is not.
- [skills/fiken/SKILL.md](skills/fiken/SKILL.md) and [skills/fiken-superforing/SKILL.md](skills/fiken-superforing/SKILL.md): what the agent reads.
- [CHANGELOG.md](CHANGELOG.md).
