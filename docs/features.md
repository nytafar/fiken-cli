# Features: reconciliation, gated writes, analytics

The hand-built surface over the mirror. Every command here reads from the local SQLite mirror unless it says otherwise; run `fiken-cli sync` first.


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
- **`vat-anomaly`** — Flags sale/purchase lines whose VAT type is implausible for the side, or deviates from the modal VAT type over the trailing 12 months for that (contact, account, normalised description) group. `--min-samples` (default 3) is how many lines must back the modal.

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
- **`reconcile`** — Registers a payment against an open sale or purchase and records it in the audit log.

  _Use to settle an open sale or purchase in NOK; payments in foreign currency and payment-processor clearing entries are roadmap items._

  ```bash
  fiken-cli reconcile --sale 2888156 --amount 34000 --account 1920:10001 --dry-run
  ```
- **`log-event`** — Appends a grammar-validated event to the append-only audit log; warns on an unknown operation name but never rejects.

  _Call this from any surface (CLI or browser harness) to record a reconciliation/posting action so the whole arc stays auditable._

  ```bash
  fiken-cli log-event --operation match.confirmed --surface browser --source-ref linje:9876543
  ```

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

## Report shape and periods

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

`--min-impact-ore` drops findings below an absolute øre threshold; `--limit` (default 200) truncates `findings[]` only — `total_findings` and `summary[]` always describe the full set. Findings sort by |impact| descending, then date; text output leads with the summary block.

`--period` on `drift`, `duplicates`, `missing-bilag`, `vat-anomaly`, `mva-summary`, `rollup` and `vendor-profile` accepts `YYYY`, `YYYY-MM`, `YYYY-Qn`, `from:to` (YYYY-MM-DD), or `all`. **Unset means the current calendar year, not all history** — `window.source` records which (`flag` | `default_current_year` | `all`), and `undated_documents` counts the documents skipped for having no date. `bank-unverified` is anchored to each account's own reconciled date and takes `--as-of` instead.

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
