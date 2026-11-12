# Sync and the local mirror

`fiken-cli sync` copies twelve resources per company into a local SQLite mirror
(`~/.local/share/fiken-cli/data.db`): contacts, journal entries, transactions, purchases,
sales, invoices, credit notes, products, projects, accounts, bank accounts and the inbox.
The mirror is a cache. Delete it and sync again whenever you doubt it.

```bash
fiken-cli sync                          # every company, incremental after the first run
fiken-cli sync --company <slug>         # one company
fiken-cli sync --full --company <slug>  # refetch everything for one company and sweep deleted rows
fiken-cli sync --since 7d               # a caller-chosen window
fiken-cli doctor                        # mirror age and consistency
```

## Details

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
changed since that company's last **complete** pull of it: a pair that has never
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
