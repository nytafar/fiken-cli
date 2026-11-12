# Surface: CDP

`scripts/superforing.mjs` drives a headed Chrome or Chromium over the DevTools Protocol.
No install: Node 22 or newer. The human watches the same window, signs in when Fiken logs
the session out, and can be asked to judge a row the agent cannot.

## Get a browser on a port

Probe first; a port number says nothing about what listens on it (Electron apps such as
Obsidian answer on 9222 with the same protocol).

```sh
S=~/.claude/skills/fiken-superforing/scripts/superforing.mjs
node $S probe --port 9222      # browser + its Fiken tabs; exit 5: nothing there, or not a browser
```

On exit 5, get a browser, in this order:

1. **Connect to one that runs.** `pgrep -af remote-debugging-port` lists candidates and their
   ports; probe each.
2. **Start one** on a free port, with a profile directory kept for Fiken so the login
   survives between runs. Chrome refuses the debug port on its default profile.
   ```sh
   chromium --remote-debugging-port=9223 --user-data-dir="$HOME/.chrome-fiken"            # Linux
   /Applications/Google\ Chrome.app/Contents/MacOS/Google\ Chrome --remote-debugging-port=9223 --user-data-dir="$HOME/.chrome-fiken"
   ```
   Start it yourself when the user sits at this machine's display; otherwise hand them the
   command.
3. **The user is on another machine** (ssh, no screen sharing): they start Chrome there as
   above and open a reverse tunnel, `ssh -N -R 9223:127.0.0.1:9223 <this host>`. The script
   then runs here against `--port 9223`. Verified 2026-11-12, macOS Chrome to a Linux host.

Done when `probe` prints a `browser` line. Pass that `--port` on every command. A
`signed-out` tab, or exit 3 later: ask the user to sign in in that window, then rerun.

## Commands

Every command attaches to the tab already on `/bank/bkbf/`, or opens one, navigates to the
filtered URL, and waits for rows.

```sh
node $S list    --fra 2026-05-01 --til 2026-06-30 --buttons  # TSV: id dato linjeBelop besteBelop linjetype tittel tekst docs knyttet knapper
node $S show    13740949433 --fra … --til … --chars 420      # buttons plus the suggestion text
node $S buttons 12094587982 12094587983 --fra … --til …      # one line per id: "id<TAB>btn / btn / btn"
node $S confirm 12046895329 12046895333 --fra … --til … --log --correlation-id <run>
node $S confirm 13672073478 --label "Bekreft dato" --fra … --til … --log   # Vipps-oppgjør
node $S confirm 12094587983 --pick 11964173461 --fra … --til … --log        # duplicate-amount row: choose the candidate
node $S book    12944273344 --purchase p.json --file bilag/x.pdf --doc 13930651255 --fra … --til … --correlation-id <run>
node $S eval 'await expand(12094587983); return [...byId(12094587983).closest("details").querySelectorAll("input[type=radio]")].map(r => r.value).join(",")'
```

- `--slug` and `--account` default to `nyta` and `170218093`; `--sok`, `--kun-innbetalinger`,
  `--side` map to the URL filters in [`../reference/superforing-dom.md`](../reference/superforing-dom.md).
- `list` prints every open row on the page in one go: no truncation, no paging. `--buttons`
  opens each row and adds its buttons, which is the whole triage in one call. `--all` drops
  the `nf` filter to include finished lines.
- `<not rendered>` in a button column means the suggestion never appeared within 5 s. It is
  a failed read, not "no suggestion": rerun that id.
- **Reload.** The panel never re-evaluates a suggestion on its own, and navigating to the URL
  the tab already shows is not a reload. `confirm` and `book` reload before acting; after an
  API write pass `--reload` to `list`, `show` or `buttons` to read the new state.
- `confirm` clicks `OK, gå til neste` (or `--label`) per id and prints `OK` or `NO` with the
  buttons present. With `--log` it also writes `match.confirmed` through `fiken-cli log-event`.
  Exit 4 if any row did not confirm; the rows that did are still confirmed and logged.
- `book` takes one open utbetaling line from purchase body to confirmed line: checks the body
  against the line (NOK, `paid`, `paymentDate` = bank date, lines sum = `linjeBelop`), refuses
  an invoice number that supplier already has in the books, creates the purchase, attaches
  `--file`, deletes inbox document `--doc`, reloads, confirms, and logs each step. `--dry-run`
  stops after the checks. On exit 4 the output names the step that failed; the earlier steps
  stand, so finish that line by hand rather than rerunning `book`.
- `eval '<js>'` runs page JS with the primitives in scope, for states the commands do not cover.
- The human sees the same tab. Calls that expand rows move their view; keep exploration in
  `eval` short.
- One `Runtime.evaluate` per row, so the 45 s cap of the extension bridge does not apply.
