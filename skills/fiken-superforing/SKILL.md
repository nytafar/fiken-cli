---
name: fiken-superforing
description: "Match Fiken bank lines in Superføring (browser-only bank matching). Use when the user asks to match, reconcile or book bank lines for a period, run Superføring, finish bankavstemming for a month, or book what the bank shows. Trigger phrases: 'superføring', 'match banklinjer', 'før mai', 'bekreft linjene', 'hva står åpent i banken'."
author: "Lasse Jellum"
license: "Apache-2.0"
argument-hint: "<company-slug> <fra> <til> [surface: chrome|cdp]"
allowed-tools: "Read Bash mcp__claude-in-chrome__*"
metadata:
  fiken-cli:
    requires: "2.2.0"
---

# Fiken Superføring

Superføring is Fiken's bank-line matching panel. It has no REST API. The bank line is the
**truth**: its date is the bank date and its `linjeBelop` is the NOK amount actually charged.
Every booking starts from a live bank line and ends with that line confirmed in the panel.

## Guardrails

- **Start in the live panel.** The first action of every run is reading the open lines for
  the period from Superføring. Inbox documents, `bank-unverified` and the mirror are lookups
  *for* a line, never a starting point.
- **The request bounds the writes.** "What is open", "what is missing", "investigate" are a
  **triage**: read, report, stop. Confirming and booking start when the user asks for them,
  for the lines they named.
- **Book paid, in NOK, from the line.** Purchases are created `paid: true`, netto equal to
  `linjeBelop`, payment date equal to `linje.dato`. Fiken's own posting rules (reverse
  charge on foreign services) still apply through `vatType`.
- **Confirm only `OK, gå til neste`.** A line offering `Registrer nytt kjøp` is not ready:
  the purchase is missing or unpaid. Fix the books, reload, then confirm. Lines offering
  nothing else are **parked** and reported.
- **Log every side effect** with `fiken-cli log-event`: `--surface browser` for confirms
  keyed `linje:<id>`, `--surface api` for purchases, payments and inbox deletions. One
  `--correlation-id` per run, shaped `superforing-<slug>-<period>-<yyyymmdd>`, on every event.
- **Log a confirm only after the row is gone.** `confirm --log` does this. A hand-logged
  event for a row still open is wrong: find its `id` in `agent_events` in
  `~/.local/share/fiken-cli/fiken-core.db` and log `match.reversed --reverses <id>`.
- **The browser is shared.** The human may click while the agent works, and that is the
  point of a headed session: they can sign in and give verdicts. Re-list before every write.
  A row gone without a script confirm was the human's click: log it with `--actor <user>`.
- Writes need live mode: `FIKEN_MODE=live` in `~/.config/fiken-cli/env` or `./.env.local`.
  A refusal with exit 8 means test mode; stop and tell the user.

## Run

0. `fiken-cli version` prints 2.2.0 or newer; older, upgrade first (`fiken` skill, Prerequisites).
1. Resolve `bankAccountId` for the account: `fiken-cli bank-accounts get <slug> --agent`.
2. Open the panel for the period and read the lines. Method per surface:
   - CDP script against a headed Chrome, preferred; it starts by finding or starting the
     browser: [`surfaces/cdp.md`](surfaces/cdp.md)
   - Claude in Chrome: [`surfaces/claude-in-chrome.md`](surfaces/claude-in-chrome.md)
   Both read the same DOM; the shared facts are in
   [`reference/superforing-dom.md`](reference/superforing-dom.md).
3. **Triage.** Read every open row with its buttons (`list --buttons`) and sort it into:
   ready to confirm, needs a booking and has its receipt, needs a booking and lacks one,
   needs a verdict. Done when every row is in exactly one group with its receipt named or
   marked missing. On a triage request, report that table and stop here.
4. Work the lines in this order, so the user is interrupted once, late:
   1. Confirm the clean ones (`OK, gå til neste`, `Bekreft dato`).
   2. Book and confirm the rows a flow can fix from the books.
   3. Raise every verdict question in one batch, then act on the answers.
   4. Park the rest.
   Pick the flow for each line and follow it to the end:
   - Utbetaling with a supplier receipt: [`flows/utbetaling-kjop.md`](flows/utbetaling-kjop.md)
   - Innbetaling, Vipps- and Stripe-oppgjør, customer invoice payments: [`flows/innbetaling.md`](flows/innbetaling.md)
   - Recurring card subscription with only a first receipt: [`flows/abonnement-internt-bilag.md`](flows/abonnement-internt-bilag.md)
   - Two lines, one amount, radio candidates (double payment): [`flows/dobbeltbetaling.md`](flows/dobbeltbetaling.md)
   - `Antatt` row whose `Leverandør` differs from the bank title: [`flows/leverandor-navnebytte.md`](flows/leverandor-navnebytte.md)
5. Done when every line in the period is either confirmed or parked with a reason, and the
   parked list is in the final report with `linje.id`, date, amount and reason.

Receipts downloaded during a run go to `bilag/<documentId>.pdf` under the period's working
folder; they are throwaway once attached to the purchase. Book from the bank line: prepared
proposal files are not part of the flow.

## Adding a flow

One file under `flows/`, named after the line kind it handles. It states: how to recognise
the line (label, `linjetype`, sign), the API calls that make it confirmable, the button that
confirms it, and what to log. Add one line to step 4 above. Surfaces stay untouched.
