# Surface: CDP

`scripts/superforing.mjs` drives the user's own headed Chromium over the DevTools Protocol.
No install: Node 22 or newer, and Chromium started with `--remote-debugging-port=9222`. The
human watches the same window, can sign in when Fiken logs the session out, and can be asked
to judge a row the agent cannot.

Every command attaches to the tab already on `/bank/bkbf/`, or opens one, navigates to the
filtered URL, and waits for rows. Exit 3 with a message means signed out: tell the user,
then rerun. Exit 5 means no Chromium on the port.

```sh
S=~/.claude/skills/fiken-superforing/scripts/superforing.mjs
node $S list    --fra 2026-05-01 --til 2026-06-30            # TSV: id dato linjeBelop besteBelop linjetype tittel tekst docs knyttet
node $S buttons 12094587982 12094587983 --fra … --til …      # one line per id: "id<TAB>btn / btn / btn"
node $S show    13740949433 --fra … --til … --chars 420      # buttons plus the suggestion text; run on every row before deciding
node $S confirm 13672073478 --label "Bekreft dato" --fra … --til … --log   # Vipps-oppgjør
node $S confirm 12046895329 12046895333 --fra … --til … --log --correlation-id <run>
```

- `--slug` and `--account` default to `nyta` and `170218093`; `--sok`, `--kun-innbetalinger`,
  `--side` map to the URL filters in [`../reference/superforing-dom.md`](../reference/superforing-dom.md).
- `list` prints every open row on the page in one go: no truncation, no paging.
- `confirm` clicks `OK, gå til neste` (or `--label`) per id and prints `OK` or `NO` with the
  buttons present. With `--log` it also writes `match.confirmed` through `fiken-cli log-event`.
  Exit 4 if any row did not confirm; the rows that did are still confirmed and logged.
- After an API write, rerun the command: it re-navigates to the same URL, which is the reload.
- One `Runtime.evaluate` per row, so the 45 s cap of the extension bridge does not apply.
