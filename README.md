# Fiken CLI

Agent-native Fiken tooling: complete read coverage over a local, searchable mirror — plus the gated, idempotent, audited write and reconciliation layer no other Fiken tool has.
Reads are a commodity. Every Fiken endpoint is mirrored into local SQLite so an agent can reason over your full accounting history with SQL and full-text search instead of round-tripping a rate-limited API. The read layer is generated and disposable — re-syncable from the API at any time.

The write path is not disposable. It touches real books, so it's hand-built around a single rule: **an action against the ledger must be impossible to do twice, impossible to do unrecorded, and possible to undo.** Fiken gives you none of those — no idempotency key, no dry-run, no write audit. This CLI adds all three, plus the error-hunting and reconciliation surface that is the whole reason it exists. Every other Fiken tool stops at read-only; none reconcile, none write safely.

## Two layers

**Reads — generated, disposable.** ~150 Fiken v2 operations as composable, single-purpose commands, plus an MCP server for agents that prefer it. A local SQLite mirror with FTS answers history questions offline, so you don't hammer Fiken's single-concurrent-request limit to find out what a vendor cost last quarter. Delta-synced; rebuildable from the API at will.

**Writes & reconciliation — hand-built, irreplaceable.** Gated, idempotent, date-aligned, and written to an append-only audit log in the same call that makes the change.

## Reconciliation & error-hunting

The surface no Fiken tool has, built around bankavstemming — the most-cited Fiken pain there is — all read-only and answered from the local mirror:

- **bank-unverified** — ledger-vs-reconciled-balance gaps and postings dated after the reconciled point: *which accounts have drifted and need a session.*
- **drift** — debit≠credit imbalances and sub-krone øre-rounding drift, exact integer-øre.
- **vat-anomaly** — MVA codes implausible for the account, or off the vendor's usual pattern.
- **duplicates** — same vendor + øre amount within N days.
- **missing-bilag** — postings with no attached documentation.

`bank-unverified` is the API-side health signal — it tells you where to look. The line-by-line matching itself runs through the browser **Superføring** workflow, which uses this CLI's idempotent `commit` and records into its audit log. The CLI owns the books-side correctness; the browser harness owns the bank-line UI.

## The gated write path

Writing to the ledger is a pipeline, not a call:

- **prepare** — an inbox document becomes a posting proposal: the original bilag, the vendor's posting history, candidate accounts, plausible MVA codes.
- **validate** — the dry-run Fiken lacks: balanced debit/credit, account exists, MVA code plausible, period open, date sane. Structural checks before anything touches the books.
- **commit** — idempotent (the same bank line or document never double-books), **date-aligned to the bank line** so the posting rendezvous with the waiting line in Fiken's own reconciliation, stamped with an `X-Request-ID`, and audited in the same call.
- **reverse** — a correcting entry or soft-delete with audit linkage. Mistakes are correctable, not catastrophic.
- **reconcile** — payment/settle plus clearing-account entries for payment-processor flows.

Plus **mva-summary**, **rollup**, and **vendor-profile** — VAT-return-shaped and margin/revenue/cost views read straight off the mirror. Because Fiken holds the books of record while ecommerce analytics see only a subset of revenue, these report the *whole-business* number, not the storefront's projection of it.

## Design principles

- **The durable assets are the idempotency map and the append-only audit log — not the mirror.** They live in a separate, backed-up store; the mirror is disposable. Structure compounds; a read cache doesn't.
- **Idempotent by construction.** Fiken has no idempotency key, so the CLI owns one. Every write checks a durable source→entity map first; a re-run is a no-op, never a duplicate posting.
- **Audited by construction.** Logging is part of the write call, not a separate step you can forget. Every action records its source, rationale, confidence, the Fiken request id, and its result — so any decision is reconstructable and reversible from your own store. The CLI's own writes and the browser reconciliation harness feed the same single log.
- **Agent-native.** Composable commands, an offline searchable mirror, compact/JSON output, built to be called thousands of times a day and driven from Claude Code.
- **Reconciliation-first.** The bank-line workflow is the point; everything else serves it.

## Install (from source)

This is a self-contained Go CLI — install it with the standard Go toolchain.

```bash
git clone https://github.com/nytafar/fiken-cli && cd fiken-cli

# One-shot: installs the fiken-cli binary AND the `fiken` agent skill
./install.sh
#   FIKEN_WITH_MCP=1 ./install.sh   # also install the MCP server (fiken-mcp)
#   FIKEN_SKILL_ONLY=1 ./install.sh # only (re)install the agent skill

# …or with make / go directly:
make install        # go install ./cmd/fiken-cli  ->  $(go env GOPATH)/bin
make install-mcp    # optional MCP server (fiken-mcp)
go install ./cmd/fiken-cli
```

Verify with `fiken-cli --version`. If it's not found, add `$(go env GOPATH)/bin` to your `$PATH`.

### Agent skill

`./install.sh` copies `SKILL.md` to `~/.claude/skills/fiken/SKILL.md`, so Claude Code (and any agent that reads `~/.claude/skills/`) triggers it on Fiken requests — bankavstemming, unmatched bank transactions, rounding errors, mis-coded VAT, MVA summaries, etc. Set `FIKEN_SKILL_DIR` to install it elsewhere.

### Auth

```bash
export FIKEN_API_TOKEN=<token>   # personal API token (Fiken → Settings → API), no expiry — recommended
# …or OAuth2 (for acting on behalf of other companies):
fiken-cli auth login             # uses FIKEN_CLIENT_ID / FIKEN_CLIENT_SECRET
fiken-cli doctor                 # verify auth + connectivity
```

`auth login` listens on `127.0.0.1:8085` and, by default, tells Fiken to redirect there.
On a headless host the browser that authorizes is somewhere else, so point the callback at
a URL both sides can reach and register that same URL with the OAuth app:

```bash
# e.g. behind a Tailscale serve proxy in front of the loopback listener
tailscale serve --bg --https=8443 8085
fiken-cli auth login --redirect-uri https://<host>.<tailnet>.ts.net:8443/callback
tailscale serve --https=8443 off     # afterwards

# …or just tunnel the port and keep the localhost default
ssh -L 8085:localhost:8085 <host>
```

## Install the agent skill (Hermes, OpenClaw, other agents)

The skill is just `SKILL.md` in this repo — any agent that reads a skills directory can use it. There's no registry or public-library step; install it straight from your clone:

- **Claude Code** (default → `~/.claude/skills/fiken/`):
  ```bash
  ./install.sh                      # or: FIKEN_SKILL_ONLY=1 ./install.sh
  ```
- **Any other agent (Hermes, OpenClaw, …)** — point `FIKEN_SKILL_DIR` at that agent's skills directory:
  ```bash
  FIKEN_SKILL_DIR=<agent-skills-dir>/fiken ./install.sh
  ```
- **Multi-agent via the [`skills`](https://github.com/vercel-labs/skills) CLI** — run it against this repo/clone (not a public registry); see `npx skills install --help` for the exact source and `--agent` syntax.

Restart the agent session or gateway if the skill isn't visible immediately.

## Use with Claude Desktop

Install the MCP server, then point Claude Desktop at it:

```bash
FIKEN_WITH_MCP=1 ./install.sh    # builds + installs fiken-mcp (alongside fiken-cli + the skill)
```

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "fiken": {
      "command": "fiken-mcp",
      "env": { "FIKEN_API_TOKEN": "<your-token>" }
    }
  }
}
```

For remote/HTTP use (and claude.ai), see [MCP over HTTP](#mcp-over-http-fiken-oauth-as-the-auth-layer) below.

## MCP over HTTP (Fiken OAuth as the auth layer)

The bundled `fiken-mcp` server speaks stdio by default; it also serves **streamable HTTP** for remote/hosted agents, with **Fiken's own OAuth2 as the authorization layer**:

```bash
fiken-mcp --transport http --addr :7777                 # passthrough: use a Bearer token if present, else the local config token
fiken-mcp --transport http --addr :7777 --require-auth   # require a per-request Fiken token (multi-tenant)
# env equivalents: FIKEN_MCP_TRANSPORT=http  FIKEN_MCP_REQUIRE_AUTH=1
```

How the auth works:

- **Per-request token, forwarded to Fiken.** Each HTTP request's `Authorization: Bearer <token>` is the *caller's* Fiken OAuth access token (or personal API token). The server uses it as the bearer on the downstream Fiken API call — it acts on behalf of whoever connected, with no shared server credential. (`stdio` is unchanged: it uses the local config token.)
- **Fiken is advertised as the OAuth authorization server**, so MCP clients can discover the flow:
  - `GET /.well-known/oauth-protected-resource` → `authorization_servers: ["https://fiken.no"]` (RFC 9728)
  - `GET /.well-known/oauth-authorization-server` → Fiken's `authorize`/`token` endpoints, PKCE S256 (RFC 8414)
  - An MCP request with `--require-auth` and no token returns `401` + `WWW-Authenticate: Bearer resource_metadata=…`, the standard trigger for client-side OAuth.
- **Caveat — no dynamic client registration.** Fiken doesn't support RFC 7591, so the AS metadata omits a `registration_endpoint`. MCP clients that *require* DCR must be configured with a pre-registered `FIKEN_CLIENT_ID`/`FIKEN_CLIENT_SECRET` (a Fiken OAuth app, redirect URI on `localhost`). Plain token passthrough works regardless of how the client obtained the token.
- Behind a proxy/TLS, set `FIKEN_MCP_PUBLIC_URL=https://your-host` so the discovery metadata advertises the correct external URL.

**Serving it publicly for claude.ai / remote agents:** see [`docs/serving-publicly.md`](docs/serving-publicly.md) — claude.ai's one-click connector needs the server to act as a small DCR-capable OAuth broker in front of Fiken (Fiken has no dynamic client registration), plus a recommended **read-only** public posture for an accounting + write server.

## Authentication

Default auth is a personal API token (Settings -> API -> Personlige API-nokler in Fiken), set as FIKEN_API_TOKEN and sent as a Bearer token — the ToS-clean path for your own companies, with no expiry. For acting on behalf of other companies, OAuth2 authorization-code login is wired for FIKEN_CLIENT_ID/FIKEN_CLIENT_SECRET via 'fiken-cli auth login' (opens a browser). Fiken allows only one concurrent request per token, so all calls are serialized through a single-flight lock and a serial, resumable sync.

## Quick Start

```bash
# verify auth + connectivity (set FIKEN_API_TOKEN first, or run 'fiken-cli auth login' for OAuth2)
fiken-cli doctor

# find your companySlug — every analytical command is scoped to it
fiken-cli companies

# build the local mirror across your companies (serial + throttled; safe to re-run, resumes)
fiken-cli sync

# the headline question: what isn't matched to the bank yet?
fiken-cli bank-unverified --company fiken-demo --as-of 2026-05-31

# find imbalanced or rounding-off entries before MVA
fiken-cli drift --company fiken-demo --period 2026-05

```

## Unique Features

These capabilities aren't available in any other tool for this API.

### Reconciliation & error hunting
- **`bank-unverified`** — Lists postings on your bank accounts that aren't yet matched to the bank statement, and shows the exact ledger-vs-reconciled gap per account.

  _Reach for this first when the user asks whether their books match the bank, or to find what's blocking a clean bankavstemming._

  ```bash
  fiken-cli bank-unverified --company fiken-demo --as-of 2026-05-31 --agent
  ```
- **`drift`** — Scans journal entries for debit≠credit imbalances and sub-krone øreavrunding drift using exact integer-øre arithmetic.

  _Run before an MVA deadline to catch the lopsided or rounding-off entries that would otherwise slip into the return._

  ```bash
  fiken-cli drift --company fiken-demo --period 2026-05 --agent
  ```
- **`vat-anomaly`** — Flags sale/purchase lines whose VAT code is implausible for the account or deviates from how that vendor is usually posted.

  _Use during pre-MVA review to surface mis-coded VAT before it reaches the filed return._

  ```bash
  fiken-cli vat-anomaly --company fiken-demo --period 2026-Q1 --agent
  ```
- **`duplicates`** — Surfaces suspected double-postings: the same contact and øre amount within a date window across purchases and sales.

  _Run during error-hunting to catch the same invoice entered twice before it inflates costs or VAT._

  ```bash
  fiken-cli duplicates --window-days 5 --company fiken-demo --agent
  ```
- **`missing-bilag`** — Lists postings in a period that have no attached documentation (empty attachments).

  _Run before closing a period to find postings that still need a receipt attached._

  ```bash
  fiken-cli missing-bilag --company fiken-demo --period 2026-05 --agent
  ```

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

### BI & VAT analytics
- **`mva-summary`** — Reconstructs a VAT-return-shaped view from local lines: net basis and output/input VAT bucketed per VAT code, for any period.

  _Reach for this to preview what an MVA return will look like, or to reconcile against the official termin report._

  ```bash
  fiken-cli mva-summary --company fiken-demo --period 2026-03 --agent
  ```
- **`rollup`** — Grouped revenue/cost/margin rollups over the local mirror by month, account, project, or contact.

  _Use for BI dashboards and ad-hoc 'margin by project this quarter' questions without re-deriving from raw entities._

  ```bash
  fiken-cli rollup --by month --metric margin --company fiken-demo --agent --select rows.period,rows.margin
  ```
- **`vendor-profile`** — Shows how a vendor is usually posted: the modal account and VAT code, with frequency and last-used.

  _Use to decide the right account/VAT for a new bill from a known vendor, or to explain why a posting looks anomalous._

  ```bash
  fiken-cli vendor-profile 1234567 --company fiken-demo --agent
  ```

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

Catches debit/credit imbalances and sub-krone rounding drift across the period in exact øre.

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

Reconstructs the output/input VAT view per code so you can sanity-check before filing.

## Usage

Run `fiken-cli --help` for the full command reference and flag list.

## Commands

### account-balances

Manage account balances

- **`fiken-cli account-balances get`** - Retrieves the bookkeeping accounts and closing balances for a given date.
An account is a string with either four digits, or four digits, a colon and five digits ("reskontro").
Examples:
3020 and 1500:10001
- **`fiken-cli account-balances get-companies`** - Retrieves the specified bookkeping account and balance for a given date.

### accounts

Manage accounts

- **`fiken-cli accounts get`** - Retrieves the bookkeeping accounts for the current year
- **`fiken-cli accounts get-companies`** - Retrieves the specified bookkeping account.
An account is a string with either four digits, or four digits, a colon and five digits ("reskontro").
      Examples:
      3020 and 1500:10001

### activities

Manage activities

- **`fiken-cli activities create-activity`** - Creates a new activity
- **`fiken-cli activities delete-activity`** - Delete or archive activity with specified id.

**Behavior:**
- If the activity has no associated time entries and is not set as a default activity on any project, it will be permanently deleted
- If the activity has associated time entries or is set as a default activity on a project, it will be **archived** instead of deleted
- Archived activities are hidden from selection lists but remain accessible for historical time entries

The response is 204 in both cases (deleted or archived).
- **`fiken-cli activities get`** - Returns all activities for given company
- **`fiken-cli activities get-activity`** - Returns activity with specified id
- **`fiken-cli activities update-activity`** - Partially updates activity with provided id. Only provided fields will be updated.

### bank-accounts

Manage bank accounts

- **`fiken-cli bank-accounts create`** - Creates a new bank account. The Location response header returns the URL of the newly created bank account.
Possible types of bank accounts are NORMAL, TAX_DEDUCTION, FOREIGN, and CREDIT_CARD. The field "foreignService" should only be filled out for accounts of type FOREIGN.
- **`fiken-cli bank-accounts get`** - Retrieves all bank accounts associated with the company.
- **`fiken-cli bank-accounts get-companies`** - Retrieves specified bank account.

### bank-balances

Manage bank balances

- **`fiken-cli bank-balances <companySlug>`** - Retrieves all bank balances for the specified company.

### companies

Manage companies

- **`fiken-cli companies get`** - Returns all companies from the system that the user has access to. The user can update which companies a given app has
access to in Fiken under Brukerinnstillinger -> Sikkerhet -> Apper du har gitt tilgang til.
- **`fiken-cli companies get-company`** - Returns company associated with slug.

### contacts

Manage contacts

- **`fiken-cli contacts create`** - Creates a new contact. The Location response header returns the URL of the newly created contact.
- **`fiken-cli contacts delete`** - Deletes the contact if possible (no associated journal entries/sales/invoices/etc). If not possible to delete will set the contact to inactive
- **`fiken-cli contacts get`** - Retrieves all contacts for the specified company.
- **`fiken-cli contacts get-companies`** - Retrieves specified contact. ContactId is returned with a GET contacts call as the first returned field.
ContactId is returned in the Location response header for POST contact.
- **`fiken-cli contacts update`** - Updates an existing contact.

### credit-notes

Manage credit notes

- **`fiken-cli credit-notes add-attachment-to-draft`** - Creates and adds a new attachment to a credit note draft
- **`fiken-cli credit-notes create-counter`** - Creates the first credit note number which is then increased by one with every new credit note. By sending an empty request body the default is base number 10000 (the first credit note number will thus be 10001), but can be specified to another starting value.
- **`fiken-cli credit-notes create-draft`** - Creates a credit note draft. This draft corresponds to a draft for an "uavhengig kreditnota" in Fiken.
- **`fiken-cli credit-notes create-from-draft`** - Creates a credit note from an already created draft.
- **`fiken-cli credit-notes create-full`** - Creates a new credit note that covers the full amount of the associated invoice.
- **`fiken-cli credit-notes create-partial`** - Creates a new credit note that doesn't fully cover the total amount of the associated invoice.
- **`fiken-cli credit-notes delete-draft`** - Delete credit note draft with specified id.
- **`fiken-cli credit-notes get`** - Returns all credit notes for given company
- **`fiken-cli credit-notes get-companies`** - Returns credit note with specified id.
- **`fiken-cli credit-notes get-counter`** - Retrieves the counter for credit notes if it has been created
- **`fiken-cli credit-notes get-draft`** - Returns credit note draft with specified id.
- **`fiken-cli credit-notes get-draft-attachments`** - Returns all attachments for specified draft.
- **`fiken-cli credit-notes get-drafts`** - Returns all credit note drafts for given company.
- **`fiken-cli credit-notes send`** - Sends the specified document
- **`fiken-cli credit-notes update-draft`** - Updates credit note draft with provided id.

### general-journal-entries

Manage general journal entries

- **`fiken-cli general-journal-entries <companySlug>`** - Creates a new general journal entry (fri postering).

### groups

Manage groups

- **`fiken-cli groups <companySlug>`** - Returns all customer groups for given company

### inbox

Manage inbox

- **`fiken-cli inbox create-document`** - Upload a document to the inbox
- **`fiken-cli inbox delete-document`** - Removes the inbox document with the specified id from the inbox.
The document is moved to the recycle bin (papirkurv) and is no longer
returned by the inbox listing endpoint. Any underlying file remains
intact, so documents that were attached to a transaction stay attached
to that transaction.
- **`fiken-cli inbox get`** - Returns the contents of the inbox for given company.
- **`fiken-cli inbox get-document`** - Returns the inbox document with specified id

### invoices

Manage invoices

- **`fiken-cli invoices add-attachment-to-draft`** - Creates and adds a new attachment to an invoice draft
- **`fiken-cli invoices create`** - Creates an invoice. This corresponds to "Ny faktura" in Fiken.
There are two types of invoice lines that can be added to an invoice line: product line or free text line.
Provide a product Id if you are invoicing a product. All information regarding the price and VAT for this product will be added to the invoice.
It is however also possible to override the unit amount by sending information in both fields "productId" and "unitAmount".
An invoice line can also be a free text line meaning that no existing product will be associated with the invoiced line.
In this case all information regarding the price and VAT of the product or service to be invoiced must be provided.
- **`fiken-cli invoices create-counter`** - Creates the first invoice number which is then increased by one with every new invoice. By sending an empty request body the default is base number 10000 (the first invoice number will thus be 10001), but can be specified to another starting value.
- **`fiken-cli invoices create-draft`** - Creates an invoice draft.
- **`fiken-cli invoices create-from-draft`** - Creates an invoice from an already created draft.
- **`fiken-cli invoices delete-draft`** - Delete invoice draft with specified id.
- **`fiken-cli invoices get`** - Returns all invoices for given company. You can filter based on issue date, last modified date, customer ID, and if the invoice is settled or not.
- **`fiken-cli invoices get-companies`** - Returns invoice with specified id.
- **`fiken-cli invoices get-counter`** - Retrieves the counter for invoices if it has been created
- **`fiken-cli invoices get-draft`** - Returns invoice draft with specified id.
- **`fiken-cli invoices get-draft-attachments`** - Returns all attachments for specified draft.
- **`fiken-cli invoices get-drafts`** - Returns all invoice drafts for given company.
- **`fiken-cli invoices send`** - Sends the specified document
- **`fiken-cli invoices update`** - Updates invoice with provided id. It is possible to update the due date of an invoice
as well as if the invoice was sent manually, outside of Fiken.
- **`fiken-cli invoices update-draft`** - Updates invoice draft with provided id.

### journal-entries

Manage journal entries

- **`fiken-cli journal-entries get`** - Returns all general journal entries (posteringer) for the specified company.
- **`fiken-cli journal-entries get-journal-entry`** - Returns all journal entries within a given company's Journal Entry Service

### offers

Manage offers

- **`fiken-cli offers add-attachment-to-draft`** - Creates and adds a new attachment to an offer draft
- **`fiken-cli offers create-counter`** - Creates the first offer number which is then increased by one with every new offer. By sending an empty request body the default is base number (the first offer number will thus be 10001), but can be specified to another starting value.
- **`fiken-cli offers create-draft`** - Creates an offer draft.
- **`fiken-cli offers create-from-draft`** - Creates an offer from an already created draft.
- **`fiken-cli offers delete-draft`** - Delete offer draft with specified id.
- **`fiken-cli offers get`** - Returns all offers for given company
- **`fiken-cli offers get-companies`** - Returns offer with specified id.
- **`fiken-cli offers get-counter`** - Retrieves the counter for offers if it has been created
- **`fiken-cli offers get-draft`** - Returns offer draft with specified id.
- **`fiken-cli offers get-draft-attachments`** - Returns all attachments for specified draft.
- **`fiken-cli offers get-drafts`** - Returns all offer drafts for given company.
- **`fiken-cli offers send`** - Sends the specified document
- **`fiken-cli offers update-draft`** - Updates offer draft with provided id.

### order-confirmations

Manage order confirmations

- **`fiken-cli order-confirmations add-attachment-to-draft`** - Creates and adds a new attachment to an order confirmation draft
- **`fiken-cli order-confirmations create-counter`** - Creates the first order confirmation number which is then increased by one with every new order confirmation. By sending an empty request body the default is base number (the first order confirmation number will thus be 10001), but can be specified to another starting value.
- **`fiken-cli order-confirmations create-draft`** - Creates an order confirmation draft.
- **`fiken-cli order-confirmations create-from-draft`** - Creates an order confirmation from an already created draft.
- **`fiken-cli order-confirmations delete-draft`** - Delete order confirmation draft with specified id.
- **`fiken-cli order-confirmations get`** - Returns all order confirmations for given company
- **`fiken-cli order-confirmations get-companies`** - Returns order confirmation with specified id.
- **`fiken-cli order-confirmations get-counter`** - Retrieves the counter for order confirmations if it has been created
- **`fiken-cli order-confirmations get-draft`** - Returns order confirmation draft with specified id.
- **`fiken-cli order-confirmations get-draft-attachments`** - Returns all attachments for specified draft.
- **`fiken-cli order-confirmations get-drafts`** - Returns all order confirmation drafts for given company.
- **`fiken-cli order-confirmations update-draft`** - Updates order confirmation draft with provided id.

### products

Manage products

- **`fiken-cli products create`** - Creates a new product.
- **`fiken-cli products create-sales-report`** - Creates a report based on provided specifications.
- **`fiken-cli products delete`** - Delete product with specified id.
- **`fiken-cli products get`** - Returns all products for given company
- **`fiken-cli products get-companies`** - Returns product with specified id.
- **`fiken-cli products update`** - Updates an existing product.

### projects

Manage projects

- **`fiken-cli projects create`** - Creates a new project
- **`fiken-cli projects delete`** - Delete project with specified id.
- **`fiken-cli projects get`** - Returns all projects for given company
- **`fiken-cli projects get-companies`** - Returns project with specified id.
- **`fiken-cli projects update`** - Updates project with provided id.

### purchases

Manage purchases

- **`fiken-cli purchases add-attachment-to-draft`** - Creates and adds a new attachment to a draft
- **`fiken-cli purchases create`** - Creates a new purchase.
- **`fiken-cli purchases create-draft`** - Creates a purchase draft.
- **`fiken-cli purchases create-from-draft`** - Creates a purchase from an already created draft.
- **`fiken-cli purchases delete-draft`** - Delete draft with specified id.
- **`fiken-cli purchases get`** - Returns all purchases for given company
- **`fiken-cli purchases get-companies`** - Returns purchase with specified id.
- **`fiken-cli purchases get-draft`** - Returns draft with specified id.
- **`fiken-cli purchases get-draft-attachments`** - Returns all attachments for specified draft.
- **`fiken-cli purchases get-drafts`** - Returns all purchase drafts for given company.
- **`fiken-cli purchases update-draft`** - Updates draft with provided id.

### sales

Manage sales

- **`fiken-cli sales add-attachment-to-draft`** - Creates and adds a new attachment to a draft
- **`fiken-cli sales create`** - Creates a new sale. This corresponds to "Annet salg" in Fiken and should be used when the invoice document and invoice number have been created outside Fiken. Otherwise the invoices-endpoints should be used.
- **`fiken-cli sales create-draft`** - Creates a sale draft.
- **`fiken-cli sales create-from-draft`** - Creates a sale from an already created draft.
- **`fiken-cli sales delete-draft`** - Delete draft with specified id.
- **`fiken-cli sales get`** - Returns all sales for given company
- **`fiken-cli sales get-companies`** - Returns sale with specified id.
- **`fiken-cli sales get-draft`** - Returns draft with specified id.
- **`fiken-cli sales get-draft-attachments`** - Returns all attachments for specified draft.
- **`fiken-cli sales get-drafts`** - Returns all sale drafts for given company.
- **`fiken-cli sales update-draft`** - Updates draft with provided id.

### time-entries

Manage time entries

- **`fiken-cli time-entries create-invoice-draft-from`** - Creates an invoice draft from one or more time entries.

The time entries will be converted to invoice lines based on the specified grouping.
After successful creation, the included time entries will be marked as "in draft" and
cannot be modified until the draft is deleted or converted to an invoice.

**Grouping options:**
- activity: One invoice line per activity, summing hours across all selected time entries for that activity
- activityAndPerson: One invoice line per unique activity+person combination
- none: Each time entry becomes its own invoice line

**Line description:**
By default, the invoice line description is generated from the activity name and total hours.
If `includeTimeEntryDescriptions` is true, individual time entry descriptions are appended.
- **`fiken-cli time-entries create-time-entry`** - Creates a new time entry
- **`fiken-cli time-entries delete-time-entry`** - Delete time entry with specified id.

**Restrictions:**
- Cannot delete if the time entry has been invoiced
- Cannot delete if the time entry belongs to a closed accounting period

Returns 400 Bad Request if deletion is not allowed due to these restrictions.
- **`fiken-cli time-entries get`** - Returns all time entries for given company
- **`fiken-cli time-entries get-time-entry`** - Returns time entry with specified id
- **`fiken-cli time-entries update-time-entry`** - Partially updates time entry with provided id. Only provided fields will be updated.

### time-users

Manage time users

- **`fiken-cli time-users get`** - Returns all persons who can register time entries for the given company
- **`fiken-cli time-users get-companies`** - Returns time user with specified id

### transactions

Manage transactions

- **`fiken-cli transactions get`** - Returns all transactions for the specified company
- **`fiken-cli transactions get-companies`** - Returns given transaction with associated id. Transaction id is returned in GET calls for
sales, purchases, and journal entries.

### user

Manage user

- **`fiken-cli user`** - Returns information about the user


## Output Formats

```bash
# Human-readable table (default in terminal, JSON when piped)
fiken-cli account-balances get mock-value --date 2026-01-15

# JSON for scripting and agents
fiken-cli account-balances get mock-value --date 2026-01-15 --json

# Filter to specific fields
fiken-cli account-balances get mock-value --date 2026-01-15 --json --select id,name,status

# Dry run — show the request without sending
fiken-cli account-balances get mock-value --date 2026-01-15 --dry-run

# Agent mode — JSON + compact + no prompts in one flag
fiken-cli account-balances get mock-value --date 2026-01-15 --agent
```

## Agent Usage

This CLI is designed for AI agent consumption:

- **Non-interactive** - never prompts, every input is a flag
- **Pipeable** - `--json` output to stdout, errors to stderr
- **Filterable** - `--select id,name` returns only fields you need
- **Previewable** - `--dry-run` shows the request without sending
- **Explicit retries** - add `--idempotent` to create retries and `--ignore-missing` to delete retries when a no-op success is acceptable
- **Confirmable** - `--yes` for explicit confirmation of destructive actions
- **Piped input** - write commands can accept structured input when their help lists `--stdin`
- **Offline-friendly** - sync/search commands can use the local SQLite store when available
- **Agent-safe by default** - no colors or formatting unless `--human-friendly` is set

Exit codes: `0` success, `2` usage error, `3` not found, `4` auth error, `5` API error, `7` rate limited, `10` config error.

## Health Check

```bash
fiken-cli doctor
```

Verifies configuration, credentials, and connectivity to the API.

## Configuration

Config file: `~/.config/fiken-cli/config.toml`

Static request headers can be configured under `headers`; per-command header overrides take precedence.

Environment variables:

| Name | Kind | Required | Description |
| --- | --- | --- | --- |
| `FIKEN_API_TOKEN` | per_call | No | Set to your API credential. |
| `FIKEN_CLIENT_ID` | auth_flow_input | No | OAuth2 client ID, used only by 'auth login' when acting on behalf of other companies. Not needed if you use a personal API token. |
| `FIKEN_CLIENT_SECRET` | auth_flow_input | No | Set during initial auth setup. |

### agentcookie (optional)

If you use agentcookie to sync secrets across machines, this CLI auto-adopts agentcookie-managed credentials with no extra setup. When the daemon writes to this CLI's config, `fiken-cli doctor` reports `agentcookie: detected` and `auth-status` labels the source as `agentcookie`. Skip this section if you don't use agentcookie - the CLI works the same as any other.

## Troubleshooting
**Authentication errors (exit code 4)**
- Run `fiken-cli doctor` to check credentials
- Verify the environment variable is set: `echo $FIKEN_API_TOKEN`
**Not found errors (exit code 3)**
- Check the resource ID is correct
- Run the `list` command to see available items

### API-specific
- **HTTP 429 or a sudden ban warning** — Fiken bans on concurrent requests per token — keep --concurrency 1 (the default) and don't run two invocations at once against the same token; the built-in single-flight lock enforces this across processes.
- **A posting won't match the bank line in Fiken** — Date-align: commit dates the posting to the expected bank-statement date, not the invoice date. Re-run with the bank-line date so it rendezvous with the waiting line.
- **401 Unauthorized** — Set FIKEN_API_TOKEN to a personal API token from Fiken (Settings -> API), or run 'fiken-cli auth login' for OAuth2; confirm with 'fiken-cli doctor'.
- **commit seems to do nothing on a re-run** — That's the idempotency guard — the same source_id was already committed. Check 'fiken-cli log-event'-recorded events or the idempotency map; use a new source or 'reverse' the prior posting.