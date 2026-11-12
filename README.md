# Fiken CLI

Norsk versjon: [README.no.md](README.no.md)

Tooling for running Fiken books together with an AI agent. You keep the decisions: which
term to close, which receipt belongs to which charge, when to deliver. The agent does the
legwork through `fiken-cli` and two skills, and everything it writes is checked against the
bank line and logged.

It has three parts:

- **A local mirror of your books.** Twelve resources per company are synced into SQLite, so
  questions about history are answered offline, with SQL and full-text search, instead of
  through a rate-limited API.
- **Checks and views on top of the mirror.** Rounding drift, VAT codes that deviate from a
  supplier's pattern, duplicates, missing documentation, a VAT-return-shaped summary, and
  revenue, cost and margin rollups.
- **Bank-line matching in Superføring.** Fiken's own matching panel has no API. The
  `fiken-superforing` skill drives it in your browser while you watch: it reads the open
  lines, books paid purchases from them with the receipt attached, and confirms them.

## What you use it for

| You want to | Ask the agent | It uses |
| --- | --- | --- |
| Know which bank lines are open and what lacks a receipt | "What is open in the bank for July and August?" | `fiken-superforing`, triage: reads and reports, writes nothing |
| Book what the bank shows | "Book and confirm those lines" | `fiken-superforing`: one paid purchase per line, receipt attached, line confirmed |
| Find errors before a VAT return | "Sweep July and August for errors" | `drift`, `vat-anomaly`, `duplicates`, `missing-bilag` |
| See the VAT figure before you deliver | "What will the return for the term show?" | `mva-summary`, reconciled against the ledger |
| Look up how a supplier is usually booked | "How do we book this vendor?" | `vendor-profile`, the mirror |
| See revenue, cost or margin for a period | "Margin per month this year" | `rollup` |

You deliver the VAT return yourself in Fiken. The tooling prepares the books for it.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/nytafar/fiken-cli/main/install.sh | bash
npx skills add nytafar/fiken-cli --skill '*' --agent claude-code --global
```

The first line installs the latest release binary into `~/.local/bin`, checksum-verified.
The second installs both skills, `fiken` and `fiken-superforing`, through
[`npx skills`](https://skills.sh/).

The skills in this repository describe `fiken-cli` 2.2.0. Check with `fiken-cli version`.
If the installer gave you an older release, build from a clone:

```bash
git clone https://github.com/nytafar/fiken-cli && cd fiken-cli
./install.sh --from-source
```

Other agents, Windows, the MCP server and installer options: [docs/install.md](docs/install.md).

## First run

```bash
export FIKEN_API_TOKEN=...   # personal API token from Fiken: Settings -> API
fiken-cli doctor             # auth, connectivity, mirror health
fiken-cli companies          # find your company slug; every command is scoped to it
fiken-cli sync               # build the mirror; later runs fetch only what changed
```

OAuth2 for acting on behalf of other companies: `fiken-cli auth login`. See
[docs/configuration.md](docs/configuration.md). How sync works, windows and full refetches:
[docs/sync.md](docs/sync.md).

## A VAT term, start to finish

This is the order that works, and the order the skills follow.

1. **Find the open term.** A delivered term leaves a VAT settlement entry in the ledger, a
   paid one a payment entry. The agent reads them from the mirror and tells you which terms
   are delivered, paid, open or overdue.
2. **Triage the bank lines.** The agent opens Superføring for the term's dates and sorts
   every open line: ready to confirm, needs a booking and has its receipt, needs a booking
   and lacks one, needs your verdict. You get that table before anything is written.
3. **Book and confirm.** On your go-ahead it books each line as a paid purchase in NOK from
   the bank amount, attaches the receipt, removes the inbox copy and confirms the line. A
   receipt that is not in the Fiken inbox is looked up in your mail, after asking you which
   mail access to use.
4. **Sweep.** `drift`, `vat-anomaly` and `duplicates` for the period.
5. **Check the figure.** `mva-summary` gives the buckets of the return. The amount to trust
   is the ledger: the sum of the VAT accounts 2700 to 2713 for the period. See the known
   gaps below for why the two can differ.
6. **Deliver.** You open the return in Fiken, compare, and deliver.

## The browser part

Superføring runs in a real, visible Chrome that you are signed in to. The agent attaches to
it over the DevTools port; it does not handle your Fiken password.

- On the same machine: the agent finds a running browser with a debug port, or starts one
  with its own profile directory so the Fiken login survives between runs.
- Over ssh, without screen sharing: you start Chrome on your own machine and open a reverse
  tunnel; the agent works through it while you watch the tab.

Setup and commands: [skills/fiken-superforing/surfaces/cdp.md](skills/fiken-superforing/surfaces/cdp.md).

## What protects your books

- **Test companies by default.** Every write is refused unless the company is a Fiken test
  company. Real books need `FIKEN_MODE=live` in an untracked env file. There is no flag for
  it, and the MCP server is always in test mode.
- **Reads before writes.** A request to look, list or investigate is a triage: the agent
  reports and stops. It books and confirms only what you ask for.
- **The bank line is the truth.** A purchase is booked with the bank's date and NOK amount,
  and the body is checked against the line before it is sent.
- **No second booking of the same invoice.** Before a purchase is created, the supplier's
  invoice number is looked up in the books. Receipts that are already booked can still sit
  unused in the inbox, so the inbox proves nothing.
- **An audit log.** Every purchase, payment, inbox deletion and confirmed line is appended
  to a local log with one correlation id per run.

## Two ways the agent writes

- **`commit`, `reconcile`, `reverse`.** The guarded commands: validated, logged in the same
  call, and for `commit` deduplicated against a durable map, so the same source never books
  twice. `prepare` and `validate` build and check the proposal first.
- **The generated commands** (`purchases create`, payments, attachments). They write
  straight to Fiken. The Superføring flows use these, and supply the checks and the log
  entries themselves.

Whether Superføring should move onto `commit` is an open design question, listed in
[docs/design.md](docs/design.md) together with the other gaps in the write path.

## Known gaps

Stated so that nothing above reads as a guarantee it is not. Each has an issue.

- `mva-summary` does not subtract credit notes, so a period with a credited invoice
  overstates output VAT ([#24](https://github.com/nytafar/fiken-cli/issues/24)).
- `purchases create` has no built-in guard against an invoice number that is already
  booked; the skill does the lookup ([#25](https://github.com/nytafar/fiken-cli/issues/25))
  and has to look the new purchase up afterwards, because the command returns no id
  ([#26](https://github.com/nytafar/fiken-cli/issues/26)).
- `missing-bilag` also lists payments, settlements and salary entries, which need no
  documentation of their own ([#28](https://github.com/nytafar/fiken-cli/issues/28)).
- `bank-unverified` is anchored to the reconciled date Fiken reports per account. When that
  date is old, the list is long and says little; the open lines are in Superføring.
- There is no command yet for term status ([#30](https://github.com/nytafar/fiken-cli/issues/30))
  or for recurring suppliers with a missing month ([#31](https://github.com/nytafar/fiken-cli/issues/31)).

## Documentation

- [docs/features.md](docs/features.md): the checks, the write commands, the analytics, report shape, periods and recipes.
- [docs/sync.md](docs/sync.md): the mirror, incremental sync, windows and full refetches.
- [docs/commands.md](docs/commands.md): the full command reference, output formats, agent flags and exit codes.
- [docs/configuration.md](docs/configuration.md): auth, the write guard and live mode, config file, troubleshooting.
- [docs/install.md](docs/install.md): installer options, other agents, from source, developing the skills.
- [docs/mcp.md](docs/mcp.md): `fiken-mcp` for Claude Code and Claude Desktop; [docs/serving-publicly.md](docs/serving-publicly.md) for a public endpoint.
- [docs/design.md](docs/design.md): design principles and the roadmap of known gaps.
- [skills/fiken/SKILL.md](skills/fiken/SKILL.md) and [skills/fiken-superforing/SKILL.md](skills/fiken-superforing/SKILL.md): what the agent reads.
- [CHANGELOG.md](CHANGELOG.md).
