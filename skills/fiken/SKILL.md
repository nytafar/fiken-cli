---
name: fiken
description: "Agent-native Fiken (Norwegian accounting) CLI: bank reconciliation, error/VAT/rounding detectors, BI rollups, and a gated/audited/idempotent write layer over an offline SQLite mirror. Use when the user asks about Fiken books, bankavstemming, unmatched bank transactions, rounding errors, mis-coded VAT, MVA/VAT summaries, missing bilag, duplicate postings, or wants to post/reverse entries safely. Trigger phrases: 'what transactions aren't matched to the bank in fiken', 'check my fiken books for rounding errors', 'bankavstemming', 'find mis-coded VAT before MVA', 'VAT summary for the period', 'margin by month', 'use fiken', 'run fiken'."
author: "Lasse Jellum"
license: "Apache-2.0"
argument-hint: "<command> [args] | install cli|mcp"
allowed-tools: "Read Bash"
metadata:
  openclaw:
    requires:
      bins:
        - fiken-cli
---

# Fiken CLI

## Prerequisites: Install the CLI

This skill drives the `fiken-cli` binary. **You must verify the CLI is installed before invoking any command from this skill.** If it is missing, install it from source:

```bash
# From a clone of this repo:
make install          # go install ./cmd/fiken-cli  -> $GOPATH/bin (usually on PATH)
make install-mcp      # optional: the MCP server (fiken-mcp)

# Or one-shot installer (binary + this skill):
./install.sh

# Or directly with the Go toolchain:
go install ./cmd/fiken-cli
```

Verify: `fiken-cli --version`. If it reports "command not found", `$(go env GOPATH)/bin` is not on `$PATH` — add it. Do not proceed with skill commands until verification succeeds.

Every Fiken read endpoint as a composable command with offline FTS and a local mirror agents can query with SQL. On top of that, an error-hunting and reconciliation surface — bank-unverified, drift, vat-anomaly, mva-summary, rollup — and a heavily gated write path (prepare, validate, commit, reverse) that is idempotent, date-aligned to the bank line, and fully audited in an append-only log. Existing Fiken tools are read-only or abandoned; none reconcile, none write safely.

## When to Use This CLI

Use this CLI when an agent needs to answer questions about a Fiken company's books (is everything matched to the bank? are there rounding errors, mis-coded VAT, missing receipts, or duplicate postings?), pull period/VAT/margin data for BI, or safely post and reverse entries with idempotency and a full audit trail. It is the right choice over raw API calls whenever the answer needs a cross-entity join, exact øre arithmetic, or a guarded write.

## Unique Capabilities

These capabilities aren't available in any other tool for this API.

### Reconciliation & error hunting
- **`bank-unverified`** — Lists postings on your bank accounts that aren't yet matched to the bank statement, and shows the exact ledger-vs-reconciled gap per account.

  _Reach for this first when the user asks whether their books match the bank, or to find what's blocking a clean bankavstemming._

  ```bash
  fiken-cli bank-unverified --company fiken-demo --as-of 2026-05-31 --agent
  ```
- **`drift`** — Scans sales and purchases for rounding/total drift and VAT-regime breaches using exact integer-øre arithmetic; journal debit/credit imbalance is not checked, because the mirrored journal lines carry no debit/credit direction.

  _Run before an MVA deadline to catch the rounding and VAT-regime slips that would otherwise reach the return._ Kinds: `total_mismatch`, the per-`vatType` invariants (`vat_rate`, `basis_has_vat`, `direct_has_net`, `nondeductible_embedded_vat`, `zero_rated_has_vat`, `unknown_vat_type`), `settled_residual`, `currency_mismatch`. `--tolerance-ore` (default 5) is a flat øre threshold.

  ```bash
  fiken-cli drift --company fiken-demo --period 2026-05 --agent
  ```
- **`vat-anomaly`** — Flags sale/purchase lines whose VAT type is implausible for the side, or deviates from the modal VAT type over the trailing 12 months for that (contact, account, normalised description) group. `--min-samples` (default 3) is how many lines must back the modal before a deviation is reported.

  _Use during pre-MVA review to surface mis-coded VAT before it reaches the filed return._

  ```bash
  fiken-cli vat-anomaly --company fiken-demo --period 2026-Q1 --agent
  ```
- **`duplicates`** — Surfaces suspected double-postings: the same contact and øre amount within a date window across purchases and sales.

  _Run during error-hunting to catch the same invoice entered twice before it inflates costs or VAT._

  ```bash
  fiken-cli duplicates --window-days 5 --company fiken-demo --agent
  ```
- **`missing-bilag`** — Scans purchases and journal entries in a period whose `attachments` array is missing or empty, reporting each document's amount as its impact.

  _Run before closing a period to find postings that still need a receipt attached; the biggest undocumented amounts sort first._

  ```bash
  fiken-cli missing-bilag --company fiken-demo --period 2026-05 --agent
  ```

### Report shape

`drift`, `duplicates`, `missing-bilag`, `vat-anomaly` and `bank-unverified` emit one envelope, inside the standard provenance wrapper:

```json
{"results": {"company": "...", "detector": "drift",
             "window": {"from": "2026-01-01", "to": "2026-12-31", "source": "flag"},
             "total_findings": 12, "undated_documents": 0, "params": {},
             "summary": [{"key": {}, "count": 3, "impact_ore": -450, "severity": "error"}],
             "findings": [{"kind": "...", "severity": "...", "doc_type": "...", "doc_id": 1, "date": "...",
                           "contact_id": 1, "contact_name": "...", "description": "...", "account": "...",
                           "vat_type": "...", "impact_ore": -150, "detail": {}}]},
 "meta": {"source": "local", "reason": "user_requested"}}
```

Shared flags: `--min-impact-ore` drops findings below an absolute øre threshold; `--limit` (default 200) truncates `findings[]` only — `total_findings` and `summary[]` always describe the full set. Findings sort by |impact| descending, then date. Text output leads with the summary block.

**Periods.** `--period` on `drift`, `duplicates`, `missing-bilag`, `vat-anomaly`, `mva-summary`, `rollup` and `vendor-profile` accepts `YYYY`, `YYYY-MM`, `YYYY-Qn`, `from:to` (YYYY-MM-DD), or `all`. **Unset means the current calendar year, not all history** — `window.source` says which (`flag` | `default_current_year` | `all`), and `undated_documents` counts the documents skipped for having no date. `bank-unverified` is anchored to each account's own reconciled date and takes `--as-of` instead.

### Gated audited writes
- **`validate`** — Dry-runs a proposed posting before it touches the books: balanced debit/credit in øre, account exists, MVA code plausible for the account, period open, date sane.

  _Always run before commit; it turns a risky write into a checked one and explains exactly why a proposal would be rejected._

  ```bash
  fiken-cli validate --file proposal.json --agent
  ```
- **`commit`** — Posts a validated purchase or journal entry through a guarded path: deduped against the idempotency map, date-aligned to the expected bank-line date, X-Request-ID stamped, and recorded in an append-only audit log in the same call.

  _The only safe way to write to the books from an agent; --dry-run shows the exact request first, and a repeat of the same source no-ops._

  ```bash
  fiken-cli commit --file proposal.json --dry-run
  ```
- **`prepare`** — Turns an inbox document into a structured posting proposal: pulls the bilag, the vendor's posting history, candidate accounts, and the valid MVA codes in one call.

  _Start here when posting a receipt from the inbox — it hands the agent everything needed to draft a correct entry, then feed it to validate and commit._

  ```bash
  fiken-cli prepare 734083065 --company fiken-demo --agent
  ```
- **`reverse`** — Reverses a posting with a correcting entry or soft-delete, recording the link back to the original in the audit log.

  _Use to undo a mistaken posting safely, leaving a complete audit chain from original to reversal._

  ```bash
  fiken-cli reverse 734083065 --company fiken-demo --dry-run
  ```
- **`reconcile`** — Registers and settles payments on sales and purchases, including clearing-account journal entries for payment processors.

  _Use when a payout or card settlement needs to be matched to an open sale/purchase and booked to the right clearing account._

  ```bash
  fiken-cli reconcile --sale 2888156 --amount 34000 --account 1920:10001 --dry-run
  ```
- **`log-event`** — Appends a grammar-validated event to the append-only audit log; warns on an unknown operation name but never rejects.

  _Call this from any surface (CLI or browser harness) to record a reconciliation/posting action so the whole arc stays auditable._

  ```bash
  fiken-cli log-event --operation match.confirmed --surface browser --source-ref linje:9876543
  ```

### Write guard and live mode

Every mutating request is refused unless the synced company record has `testCompany: true`. A refusal exits **8** with a JSON error naming the company slug and a reason code: `not_test_company`, `company_unknown` (the slug is absent from the local mirror — build it first), or `guard_unconfigured` (the fail-closed default). Nothing goes on the wire.

Writing to real books requires live mode, which is reached only by `FIKEN_MODE` set to `live` in an untracked env file (`.env.local` or `.env` in the working directory, or `~/.config/fiken-cli/env`) or in the process environment. **There is deliberately no CLI flag**: `--agent` never changes the write mode — only `FIKEN_MODE` in the process environment or an untracked env file does — and the MCP server is always in test mode.

### BI & VAT analytics
- **`mva-summary`** — Reconstructs a VAT-return-shaped view from local lines: net basis and output/input VAT bucketed by (`vatType`, `mva_code`), for any period.

  _Reach for this to preview what an MVA return will look like, or to reconcile against the official termin report._ Also carries `findings[]` for reverse-charge buckets that disagree with the journal's 2702/2712 pair, and `transactions_indexed`.

  ```bash
  fiken-cli mva-summary --company fiken-demo --period 2026-03 --agent
  ```
- **`rollup`** — Grouped revenue/cost/margin rollups over the local mirror by month, account, or contact. `--by` takes only those three; there is no project dimension, because the mirrored sales/purchase lines carry no project.

  _Use for BI dashboards and ad-hoc 'margin by month this quarter' questions without re-deriving from raw entities._

  ```bash
  fiken-cli rollup --by month --metric margin --company fiken-demo --agent --select rows.label,rows.margin
  ```
- **`vendor-profile`** — Shows how a vendor is usually posted: the modal account and VAT code, with frequency and last-used. Takes `--period`; the two modals are bounded to the last `modal_window_months` (12) of purchases inside it.

  _Use to decide the right account/VAT for a new bill from a known vendor, or to explain why a posting looks anomalous._

  ```bash
  fiken-cli vendor-profile 1234567 --company fiken-demo --agent
  ```

## Command Reference

**account-balances** — Manage account balances

- `fiken-cli account-balances get` — Retrieves the bookkeeping accounts and closing balances for a given date.
- `fiken-cli account-balances get-companies` — Retrieves the specified bookkeping account and balance for a given date.

**accounts** — Manage accounts

- `fiken-cli accounts get` — Retrieves the bookkeeping accounts for the current year
- `fiken-cli accounts get-companies` — Retrieves the specified bookkeping account.

**activities** — Manage activities

- `fiken-cli activities create-activity` — Creates a new activity
- `fiken-cli activities delete-activity` — Delete or archive activity with specified id.
- `fiken-cli activities get` — Returns all activities for given company
- `fiken-cli activities get-activity` — Returns activity with specified id
- `fiken-cli activities update-activity` — Partially updates activity with provided id. Only provided fields will be updated.

**bank-accounts** — Manage bank accounts

- `fiken-cli bank-accounts create` — Creates a new bank account. The Location response header returns the URL of the newly created bank account.
- `fiken-cli bank-accounts get` — Retrieves all bank accounts associated with the company.
- `fiken-cli bank-accounts get-companies` — Retrieves specified bank account.

**bank-balances** — Manage bank balances

- `fiken-cli bank-balances <companySlug>` — Retrieves all bank balances for the specified company.

**companies** — Manage companies

- `fiken-cli companies get` — Returns all companies from the system that the user has access to.
- `fiken-cli companies get-company` — Returns company associated with slug.

**contacts** — Manage contacts

- `fiken-cli contacts create` — Creates a new contact. The Location response header returns the URL of the newly created contact.
- `fiken-cli contacts delete` — Deletes the contact if possible (no associated journal entries/sales/invoices/etc).
- `fiken-cli contacts get` — Retrieves all contacts for the specified company.
- `fiken-cli contacts get-companies` — Retrieves specified contact. ContactId is returned with a GET contacts call as the first returned field.
- `fiken-cli contacts update` — Updates an existing contact.

**credit-notes** — Manage credit notes

- `fiken-cli credit-notes add-attachment-to-draft` — Creates and adds a new attachment to a credit note draft
- `fiken-cli credit-notes create-counter` — Creates the first credit note number which is then increased by one with every new credit note.
- `fiken-cli credit-notes create-draft` — Creates a credit note draft. This draft corresponds to a draft for an 'uavhengig kreditnota' in Fiken.
- `fiken-cli credit-notes create-from-draft` — Creates a credit note from an already created draft.
- `fiken-cli credit-notes create-full` — Creates a new credit note that covers the full amount of the associated invoice.
- `fiken-cli credit-notes create-partial` — Creates a new credit note that doesn't fully cover the total amount of the associated invoice.
- `fiken-cli credit-notes delete-draft` — Delete credit note draft with specified id.
- `fiken-cli credit-notes get` — Returns all credit notes for given company
- `fiken-cli credit-notes get-companies` — Returns credit note with specified id.
- `fiken-cli credit-notes get-counter` — Retrieves the counter for credit notes if it has been created
- `fiken-cli credit-notes get-draft` — Returns credit note draft with specified id.
- `fiken-cli credit-notes get-draft-attachments` — Returns all attachments for specified draft.
- `fiken-cli credit-notes get-drafts` — Returns all credit note drafts for given company.
- `fiken-cli credit-notes send` — Sends the specified document
- `fiken-cli credit-notes update-draft` — Updates credit note draft with provided id.

**general-journal-entries** — Manage general journal entries

- `fiken-cli general-journal-entries <companySlug>` — Creates a new general journal entry (fri postering).

**groups** — Manage groups

- `fiken-cli groups <companySlug>` — Returns all customer groups for given company

**inbox** — Manage inbox

- `fiken-cli inbox create-document` — Upload a document to the inbox
- `fiken-cli inbox delete-document` — Removes the inbox document with the specified id from the inbox.
- `fiken-cli inbox get` — Returns the contents of the inbox for given company.
- `fiken-cli inbox get-document` — Returns the inbox document with specified id

**invoices** — Manage invoices

- `fiken-cli invoices add-attachment-to-draft` — Creates and adds a new attachment to an invoice draft
- `fiken-cli invoices create` — Creates an invoice. This corresponds to 'Ny faktura' in Fiken.
- `fiken-cli invoices create-counter` — Creates the first invoice number which is then increased by one with every new invoice.
- `fiken-cli invoices create-draft` — Creates an invoice draft.
- `fiken-cli invoices create-from-draft` — Creates an invoice from an already created draft.
- `fiken-cli invoices delete-draft` — Delete invoice draft with specified id.
- `fiken-cli invoices get` — Returns all invoices for given company.
- `fiken-cli invoices get-companies` — Returns invoice with specified id.
- `fiken-cli invoices get-counter` — Retrieves the counter for invoices if it has been created
- `fiken-cli invoices get-draft` — Returns invoice draft with specified id.
- `fiken-cli invoices get-draft-attachments` — Returns all attachments for specified draft.
- `fiken-cli invoices get-drafts` — Returns all invoice drafts for given company.
- `fiken-cli invoices send` — Sends the specified document
- `fiken-cli invoices update` — Updates invoice with provided id.
- `fiken-cli invoices update-draft` — Updates invoice draft with provided id.

**journal-entries** — Manage journal entries

- `fiken-cli journal-entries get` — Returns all general journal entries (posteringer) for the specified company.
- `fiken-cli journal-entries get-journal-entry` — Returns all journal entries within a given company's Journal Entry Service

**offers** — Manage offers

- `fiken-cli offers add-attachment-to-draft` — Creates and adds a new attachment to an offer draft
- `fiken-cli offers create-counter` — Creates the first offer number which is then increased by one with every new offer.
- `fiken-cli offers create-draft` — Creates an offer draft.
- `fiken-cli offers create-from-draft` — Creates an offer from an already created draft.
- `fiken-cli offers delete-draft` — Delete offer draft with specified id.
- `fiken-cli offers get` — Returns all offers for given company
- `fiken-cli offers get-companies` — Returns offer with specified id.
- `fiken-cli offers get-counter` — Retrieves the counter for offers if it has been created
- `fiken-cli offers get-draft` — Returns offer draft with specified id.
- `fiken-cli offers get-draft-attachments` — Returns all attachments for specified draft.
- `fiken-cli offers get-drafts` — Returns all offer drafts for given company.
- `fiken-cli offers send` — Sends the specified document
- `fiken-cli offers update-draft` — Updates offer draft with provided id.

**order-confirmations** — Manage order confirmations

- `fiken-cli order-confirmations add-attachment-to-draft` — Creates and adds a new attachment to an order confirmation draft
- `fiken-cli order-confirmations create-counter` — Creates the first order confirmation number which is then increased by one with every new order confirmation.
- `fiken-cli order-confirmations create-draft` — Creates an order confirmation draft.
- `fiken-cli order-confirmations create-from-draft` — Creates an order confirmation from an already created draft.
- `fiken-cli order-confirmations delete-draft` — Delete order confirmation draft with specified id.
- `fiken-cli order-confirmations get` — Returns all order confirmations for given company
- `fiken-cli order-confirmations get-companies` — Returns order confirmation with specified id.
- `fiken-cli order-confirmations get-counter` — Retrieves the counter for order confirmations if it has been created
- `fiken-cli order-confirmations get-draft` — Returns order confirmation draft with specified id.
- `fiken-cli order-confirmations get-draft-attachments` — Returns all attachments for specified draft.
- `fiken-cli order-confirmations get-drafts` — Returns all order confirmation drafts for given company.
- `fiken-cli order-confirmations update-draft` — Updates order confirmation draft with provided id.

**products** — Manage products

- `fiken-cli products create` — Creates a new product.
- `fiken-cli products create-sales-report` — Creates a report based on provided specifications.
- `fiken-cli products delete` — Delete product with specified id.
- `fiken-cli products get` — Returns all products for given company
- `fiken-cli products get-companies` — Returns product with specified id.
- `fiken-cli products update` — Updates an existing product.

**projects** — Manage projects

- `fiken-cli projects create` — Creates a new project
- `fiken-cli projects delete` — Delete project with specified id.
- `fiken-cli projects get` — Returns all projects for given company
- `fiken-cli projects get-companies` — Returns project with specified id.
- `fiken-cli projects update` — Updates project with provided id.

**purchases** — Manage purchases

- `fiken-cli purchases add-attachment-to-draft` — Creates and adds a new attachment to a draft
- `fiken-cli purchases create` — Creates a new purchase.
- `fiken-cli purchases create-draft` — Creates a purchase draft.
- `fiken-cli purchases create-from-draft` — Creates a purchase from an already created draft.
- `fiken-cli purchases delete-draft` — Delete draft with specified id.
- `fiken-cli purchases get` — Returns all purchases for given company
- `fiken-cli purchases get-companies` — Returns purchase with specified id.
- `fiken-cli purchases get-draft` — Returns draft with specified id.
- `fiken-cli purchases get-draft-attachments` — Returns all attachments for specified draft.
- `fiken-cli purchases get-drafts` — Returns all purchase drafts for given company.
- `fiken-cli purchases update-draft` — Updates draft with provided id.

**sales** — Manage sales

- `fiken-cli sales add-attachment-to-draft` — Creates and adds a new attachment to a draft
- `fiken-cli sales create` — Creates a new sale.
- `fiken-cli sales create-draft` — Creates a sale draft.
- `fiken-cli sales create-from-draft` — Creates a sale from an already created draft.
- `fiken-cli sales delete-draft` — Delete draft with specified id.
- `fiken-cli sales get` — Returns all sales for given company
- `fiken-cli sales get-companies` — Returns sale with specified id.
- `fiken-cli sales get-draft` — Returns draft with specified id.
- `fiken-cli sales get-draft-attachments` — Returns all attachments for specified draft.
- `fiken-cli sales get-drafts` — Returns all sale drafts for given company.
- `fiken-cli sales update-draft` — Updates draft with provided id.

**time-entries** — Manage time entries

- `fiken-cli time-entries create-invoice-draft-from` — Creates an invoice draft from one or more time entries.
- `fiken-cli time-entries create-time-entry` — Creates a new time entry
- `fiken-cli time-entries delete-time-entry` — Delete time entry with specified id.
- `fiken-cli time-entries get` — Returns all time entries for given company
- `fiken-cli time-entries get-time-entry` — Returns time entry with specified id
- `fiken-cli time-entries update-time-entry` — Partially updates time entry with provided id. Only provided fields will be updated.

**time-users** — Manage time users

- `fiken-cli time-users get` — Returns all persons who can register time entries for the given company
- `fiken-cli time-users get-companies` — Returns time user with specified id

**transactions** — Manage transactions

- `fiken-cli transactions get` — Returns all transactions for the specified company
- `fiken-cli transactions get-companies` — Returns given transaction with associated id.

**user** — Manage user

- `fiken-cli user` — Returns information about the user


### Finding the right command

When you know what you want to do but not which command does it, ask the CLI directly:

```bash
fiken-cli which "<capability in your own words>"
```

`which` resolves a natural-language capability query to the best matching command from this CLI's curated feature index. Exit code `0` means at least one match; exit code `2` means no confident match — fall back to `--help` or use a narrower query.

## Recipes

### Find what's blocking bankavstemming

```bash
fiken-cli bank-unverified --company fiken-demo --as-of 2026-05-31 --agent
```

Shows the ledger-vs-reconciled gap per bank account and the postings dated after the last reconciled date — the #1 reconciliation question.

### Pre-MVA error sweep

```bash
fiken-cli drift --company fiken-demo --period 2026-05 --agent
```

Catches sub-krone rounding drift, header-vs-lines mismatches and VAT-regime breaches across the period in exact øre. Without `--period` it covers the current calendar year.

### BI rollup narrowed for agents

```bash
fiken-cli rollup --by month --metric margin --company fiken-demo --agent --select rows.label,rows.margin
```

Monthly margin from the local mirror, projected to just the two fields an agent needs so it doesn't parse the full payload.

### Safe receipt posting

```bash
fiken-cli prepare 734083065 --company fiken-demo --agent
```

Builds a posting proposal from an inbox document; pipe it to validate, then commit --dry-run before the real write.

### VAT return preview

```bash
fiken-cli mva-summary --company fiken-demo --period 2026-03 --agent
```

Reconstructs the output/input VAT view per (`vatType`, `mva_code`) so you can sanity-check before filing.

## Auth Setup

Default auth is a personal API token (Settings -> API -> Personlige API-nokler in Fiken), set as FIKEN_API_TOKEN and sent as a Bearer token — the ToS-clean path for your own companies, with no expiry. For acting on behalf of other companies, OAuth2 authorization-code login is wired for FIKEN_CLIENT_ID/FIKEN_CLIENT_SECRET via 'fiken-cli auth login' (opens a browser). Fiken allows only one concurrent request per token, so all calls are serialized through a single-flight lock and a serial, resumable sync.

Run `fiken-cli doctor` to verify setup.

## Agent Mode

Add `--agent` to any command. Expands to: `--json --compact --no-input --no-color --yes`.

- **Pipeable** — JSON on stdout, errors on stderr
- **Filterable** — `--select` keeps a subset of fields. Dotted paths descend into nested structures; arrays traverse element-wise. Critical for keeping context small on verbose APIs:

  ```bash
  fiken-cli account-balances get mock-value --date 2026-01-15 --agent --select id,name,status
  ```
- **Previewable** — `--dry-run` shows the request without sending
- **Offline-friendly** — sync/search commands can use the local SQLite store when available
- **Non-interactive** — never prompts, every input is a flag
- **Explicit retries** — use `--idempotent` only when an already-existing create should count as success, and `--ignore-missing` only when a missing delete target should count as success

### Response envelope

Commands that read from the local store or the API wrap output in a provenance envelope:

```json
{
  "meta": {"source": "live" | "local", "synced_at": "...", "reason": "..."},
  "results": <data>
}
```

Parse `.results` for data and `.meta.source` to know whether it's live or local. A human-readable `N results (live)` summary is printed to stderr only when stdout is a terminal AND no machine-format flag (`--json`, `--csv`, `--compact`, `--quiet`, `--plain`, `--select`) is set — piped/agent consumers and explicit-format runs get pure JSON on stdout.

## Agent Feedback

When you (or the agent) notice something off about this CLI, record it:

```
fiken-cli feedback "the --since flag is inclusive but docs say exclusive"
fiken-cli feedback --stdin < notes.txt
fiken-cli feedback list --json --limit 10
```

Entries are stored locally at `~/.local/share/fiken-cli/feedback.jsonl`. They are never POSTed unless `FIKEN_FEEDBACK_ENDPOINT` is set AND either `--send` is passed or `FIKEN_FEEDBACK_AUTO_SEND=true`. Default behavior is local-only.

Write what *surprised* you, not a bug report. Short, specific, one line: that is the part that compounds.

## Output Delivery

Every command accepts `--deliver <sink>`. The output goes to the named sink in addition to (or instead of) stdout, so agents can route command results without hand-piping. Three sinks are supported:

| Sink | Effect |
|------|--------|
| `stdout` | Default; write to stdout only |
| `file:<path>` | Atomically write output to `<path>` (tmp + rename) |
| `webhook:<url>` | POST the output body to the URL (`application/json` or `application/x-ndjson` when `--compact`) |

Unknown schemes are refused with a structured error naming the supported set. Webhook failures return non-zero and log the URL + HTTP status on stderr.

## Named Profiles

A profile is a saved set of flag values, reused across invocations. Use it when a scheduled agent calls the same command every run with the same configuration - HeyGen's "Beacon" pattern.

```
fiken-cli profile save briefing --json
fiken-cli --profile briefing account-balances get mock-value --date 2026-01-15
fiken-cli profile list --json
fiken-cli profile show briefing
fiken-cli profile delete briefing --yes
```

Explicit flags always win over profile values; profile values win over defaults. `agent-context` lists all available profiles under `available_profiles` so introspecting agents discover them at runtime.

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 2 | Usage error (wrong arguments) |
| 3 | Resource not found |
| 4 | Authentication required |
| 5 | API error (upstream issue) |
| 7 | Rate limited (wait and retry) |
| 8 | Write refused by the test-company guard (nothing was sent) |
| 10 | Config error |

## Argument Parsing

Parse `$ARGUMENTS`:

1. **Empty, `help`, or `--help`** → show `fiken-cli --help` output
2. **Starts with `install`** → ends with `mcp` → MCP installation; otherwise → see Prerequisites above
3. **Anything else** → Direct Use (execute as CLI command with `--agent`)

## MCP Server Installation

Install the MCP binary from this repo (`FIKEN_WITH_MCP=1 ./install.sh`, or `make install-mcp`), then register it:

```bash
claude mcp add fiken-mcp -- fiken-mcp
```

Verify: `claude mcp list`

## Direct Use

1. Check if installed: `which fiken-cli`
   If not found, offer to install (see Prerequisites at the top of this skill).
2. Match the user query to the best command from the Unique Capabilities and Command Reference above.
3. Execute with the `--agent` flag:
   ```bash
   fiken-cli <command> [subcommand] [args] --agent
   ```
4. If ambiguous, drill into subcommand help: `fiken-cli <command> --help`.
