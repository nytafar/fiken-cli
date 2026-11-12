# Changelog

This file is maintained by printing-press-library release automation. Do not hand-edit release sections in normal PRs.

## Unreleased

## 2.3.0 - 2026-11-12

### Added

- `fiken-superforing`: `superforing.mjs probe` reports what answers on the DevTools port and refuses an Electron app; `list --buttons` prints each open line with its buttons; `book` takes one open line from purchase body to confirmed line (checks against the line, duplicate guard on supplier and invoice number, attach, inbox delete, reload, confirm, logged); `--reload` on the read commands.
- `fiken-superforing`: a triage step that reads, reports and stops; a purchase-date rule per MVA-termin; a mail lookup before a line is parked for a missing receipt; connecting to a running browser, starting one, or working through an ssh reverse tunnel.
- `fiken` skill: a version check, a pointer to `fiken-superforing` for bank lines, and a recipe for preparing an MVA-termin.
- `README.no.md`, a Norwegian README. `docs/sync.md` holds the sync details that lived in the README.
- `mva-status` lists a company's VAT terms for a year, one row per term from its `vatType` (bi-monthly, monthly, yearly): period, due date moved off weekends and public holidays, delivered date and amount, paid date and amount, what is outstanding, what sits on the VAT accounts, and a state of `paid`, `delivered`, `open` or `overdue`. Findings report each overdue term, VAT posted into a term after its return was delivered, and a balance on account 2740 the listed terms do not explain. `--date` says whether the term a date falls in is closed (#30).
- `recurring-gaps` reports, for each supplier and account booked in at least `--min-months` (3) of the `--lookback` (6) months before a month, the months in the period with no purchase, with the usual amount and the last booking. A series whose last booking is more than `--ended-after` (3) months back is reported as `recurring_ended`, and an unused inbox document from the supplier in the gap month is noted on the finding (#31).
- Every create reports what it made: the JSON envelope of a POST command carries `id` and `location`, read from the `Location` header Fiken answers a create with, so `purchases create … --agent | jq .id` prints the new purchaseId. A numeric id is a JSON number, a draft's UUID a string. `commit` records the id from the same header when the response has no body (#26).
- `purchases create` and `commit` refuse a purchase whose supplier already has the invoice number: the supplier and `identifier` are looked up in the live API for purchases dated within 92 days and in the mirror for every date, and a hit exits `9` without posting, naming the existing purchase, its date and whether it is paid. `--idempotent` turns the refusal into a no-op that returns the existing id; `--allow-duplicate` skips the check. A lookup that fails refuses too. `prepare` lists `already_booked` purchases for an inbox document whose filename is on a purchase attachment or whose name carries a purchase's invoice number (#25).
- `doctor` and `version --check` compare the binary with the latest GitHub release and with the minimum version each installed fiken skill states in its frontmatter (`metadata.fiken-cli.requires`). `doctor --json` carries `latest_release`, `update_available`, `upgrade_command` and `skills[]` with `requires` and `satisfied`. The release lookup is cached for a day, times out after three seconds, is skipped with `FIKEN_NO_UPDATE_CHECK` set and never fails the command (#32).
- `doctor --company <slug>` limits the mirror checks to one company (#29).

### Changed

- `README.md` is rewritten for the operator: what to ask for, a VAT term from start to finish, what protects the books, and the known gaps with their issues.
- `missing-bilag` reports only what needs its own bilag: purchases and sales without an attachment, and journal entries not derived from a documented source (free postings, customs declarations, unknown transaction types). The posting of a purchase or sale, payments on it, payment-provider settlements, VAT settlement, salary and tax, bank fees and interest, internal transfers and year-end and system entries are counted per class under a new `excluded` field of the report instead; `--include <class,…|all>` reports a class anyway. An entry is classified on its `transactionType` and on whether its transaction is a mirrored purchase or sale. Sales without an attachment are new in the report (#28).
- `doctor` breaks the mirror findings down per company: each carries a `company` field and its fix line names the slug. Rows that name no company are reported as `unowned_rows`. The mirror-wide comparison of `sync_state.total_count` with the row count is reported on its own (#29).
- `log-event` knows `purchase.created`, `inbox.deleted`, `attachment.added`, `match.reversed` and `contact.renamed`, the operations the `fiken-superforing` skill prescribes, and a test fails on any operation a skill names that the known set lacks (#27).
- The skills state the CLI version they need in their frontmatter and point at `doctor` for the check, in place of a manual comparison against the releases API. The `fiken` skill, `README.md`, `README.no.md` and `docs/features.md` describe the new commands and drop the caveats for the issues closed here.
- `fiken` skill and `docs/features.md` no longer call `commit` the only safe write path: the Superføring flows use the generated create commands with their own checks and log entries. `mva-summary`, `missing-bilag` and `bank-unverified` carry their current limits (#24, #28).

### Fixed

- A write is sent once. The client retried every request up to three times after a timeout, a dropped connection or a 5xx answer, so a create that Fiken had carried out before the answer was lost could be booked twice inside one call. Only reads are retried now; a write returns the error, and a rerun meets the duplicate guard or `commit`'s recovery.
- `mva-summary` subtracts credit notes. A credit note is a sale with negative lines that the `sales` resource does not list, so a period with a credited invoice overstated output VAT. Its lines are folded into the sales bucket they belong to, and `credit_notes` in the report says by how much. The totals are now compared with the movement on the ledger's VAT accounts (2700–2719) in the same window: `ledger` carries the figures, a difference above one krone is a `ledger_vat_mismatch` finding, and `ledger.by_type` splits the movement by transaction type so VAT posted outside a sale or purchase is visible (#24).
- A full sync clears a company's rows in the typed tables as well as in `resources`. Rows written under an earlier id rule had no counterpart there and survived every sync, which left each inbox document in the typed `inbox` table twice and `doctor` reporting a disagreement its own fix could not clear (#29).
- `fiken-superforing`: `confirm` reloads the panel first, so a suggestion changed by an API write is no longer read stale; button reads wait for a stable, non-empty list, so the first row after a page load no longer reads as having no suggestion.

## 2.2.0 - 2026-09-11

### Added

- `install.sh` downloads the checksum-verified GitHub release archive for the host OS and arch into `~/.local/bin`; `--from-source` keeps the `go install` path, `--with-mcp` adds `fiken-mcp`, and `--skills`/`--link-skills` are opt-in. Skills install through `npx skills add` by default, so setup is two commands. The README keeps the pitch, install and quick start; the feature catalogue, command reference, configuration, MCP notes and design/roadmap move to `docs/`, which release archives now carry.
- `inbox get-document --output <path>` downloads the file behind the document's `documentUrl` through the authenticated client instead of leaving the bearer token to be curled by hand: a file path writes that name, a directory (or a trailing `/`) writes the document's own filename, `-` streams to stdout, and `--dry-run` prints the request without fetching. `purchases attachments get-purchase` and `sales attachments get-sale` take the same flag and write every attachment into the directory given. `--agent`/JSON mode prints `{"path":…,"bytes":…,"filename":…}` for one file and `{"files":[…],"count":…,"bytes":…}` for several (#23).
- A plain `sync` is now incremental: each (company, resource) pair keeps its own watermark in a new `sync_watermark` table, and a repeat run sends `lastModifiedGe` for contacts, journal_entries, transactions, products, sales, invoices and credit_notes, announcing each windowed pair with `{"event":"sync_window","company":...,"resource":...,"since":...}`. A pair that has never completed a pull, and every resource without a date filter, is still fetched in full. The watermark is the start of the run that filled it and is written only after a pull with no error and no row-count mismatch, so an interrupted run re-pulls rather than skips. `--since` stays a caller window and leaves the watermark untouched; `--full` ignores it, clears it for every pair in its scope before the first request — so a `--full` that is interrupted after it deletes rows leaves "pull me in full" rather than a mark for rows it never refetched — and writes it again from the full pull (#22).

### Changed

- Deletion detection (#17) now runs on a pair's first complete pull and on `sync --full`, not on every default run: an incremental pull asked for a subset, so an absent row is no evidence that it was deleted. Run `sync --full --company <slug>` to sweep rows deleted in Fiken. For the same reason a run that windowed any company no longer overwrites the recorded `result_count` for that resource (#22).

### Fixed

- The add-attachment commands (`purchases`/`sales`/`contacts`/`journal-entries` `attachments`, the `add-attachment-to-draft` family and `inbox create-document`) default `--filename` to the basename of `--file`, so an upload that omits it no longer fails with HTTP 400 `filename must be specified` (#23).

## 2.1.0 - 2026-09-11

### Fixed

- `sync` walks paginated resources from page 0, the spec default, instead of skipping page 1; the generated `--all` reads follow every page of a bare-array list (#12). Every mirror needs one `sync --full` after upgrading; a plain `sync` resumes from the old cursor.
- Naming a dependent resource in `sync --resources` no longer produces a phantom unnamed failure; an unknown name emits a named `sync_error`, an empty or unmatched parent table a named `sync_warning` (#14).

### Added

- The client captures Fiken's `Fiken-Api-Page`, `-Page-Size`, `-Page-Count` and `-Result-Count` headers. Paginated reads publish `result_count` and `page_count` in the provenance `meta`; `sync` compares the distinct rows landed per company and resource against the header and emits `{"event":"sync_anomaly","reason":"result_count_mismatch"}` on a short pull; an unscoped, unwindowed full sync records the count in `sync_state.result_count`, shown by `doctor` next to the row count (#15).
- `sync --since` and the `last_synced_at` watermark now send `lastModifiedGe` for contacts, journal_entries, transactions, products and sales, with the start day moved back one; the other resources have no date filter and stay full pulls, each saying so once with `resource_not_incremental` (#13).

## 2.0.0 - 2026-09-10

### Breaking

- Every detector (`drift`, `vat-anomaly`, `missing-bilag`, `validate`, `mva-summary`, …) now emits the same `Report` envelope, nested inside the existing provenance envelope. Consumers reading the old per-detector top-level shapes must be updated.
- `--period` unset now means the current year, not all history. Pass `--period all` for the previous behaviour.
- A write blocked by the test-company guard exits with code 8 (was a generic failure).

### Added

- Fail-closed write guard: mutations are denied unless the company slug resolves to a Fiken test company; live mode is opt-in through an untracked env file, never a flag.
- One VAT authority with explicit regimes, replacing the private rate tables.
- Transaction index, plus `settled_residual` and `currency_mismatch` checks and an MVA/journal cross-check for foreign-service reverse charge.
- Recency-bounded modal account inference with `--min-samples`.
- `--min-impact-ore` and `--limit` on the detectors.
- `doctor` mirror invariants: `sync_state.total_count` vs. actual rows, typed-table counts, bare storage keys and orphaned kebab-spelled populations.
- A `--company` scope for sync (and a scoped full resync that clears the company's rows before refetching), so a run cannot walk every book in the mirror.

### Fixed

- One canonical resource name across the mirror: typed tables (`journal_entries`, `bank_accounts`) actually fill, the three hyphen maps are snake-spelled, and both dispatch switches error on an unknown name instead of silently dropping rows.
- Write-through and mutation-response cache rows now carry `parent_id`, so they are visible to the company-scoped readers and two companies' rows with the same API id no longer collide.
- VAT regime false positives.
- `missing-bilag` reads the real attachment field.
- Documentation claims corrected: `drift` does not check debit≠credit imbalance and `rollup` has no project dimension.

### Notes

- One-off resync per company after upgrading: a full, company-scoped sync of `journal_entries,bank_accounts` replaces the pre-2.0 bare-keyed rows.
