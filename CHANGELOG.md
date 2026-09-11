# Changelog

This file is maintained by printing-press-library release automation. Do not hand-edit release sections in normal PRs.

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
