---
name: fiken-superforing
description: "Match Fiken bank lines in Superføring (browser-only bank matching). Use when the user asks to match, reconcile or book bank lines for a period, run Superføring, finish bankavstemming for a month, or book what the bank shows. Trigger phrases: 'superføring', 'match banklinjer', 'før mai', 'bekreft linjene', 'hva står åpent i banken'."
author: "Lasse Jellum"
license: "Apache-2.0"
argument-hint: "<company-slug> <fra> <til> [surface: chrome|cdp]"
allowed-tools: "Read Bash mcp__claude-in-chrome__*"
---

# Fiken Superføring

Superføring is Fiken's bank-line matching panel. It has no REST API. The bank line is the
**truth**: its date is the bank date and its `linjeBelop` is the NOK amount actually charged.
Every booking starts from a live bank line and ends with that line confirmed in the panel.

## Guardrails

- **Start in the live panel.** The first action of every run is reading the open lines for
  the period from Superføring. Inbox documents, `bank-unverified` and the mirror are lookups
  *for* a line, never a starting point.
- **Book paid, in NOK, from the line.** Purchases are created `paid: true`, netto equal to
  `linjeBelop`, payment date equal to `linje.dato`. Fiken's own posting rules (reverse
  charge on foreign services) still apply through `vatType`.
- **Confirm only `OK, gå til neste`.** A line offering `Registrer nytt kjøp` is not ready:
  the purchase is missing or unpaid. Fix the books, reload, then confirm. Lines offering
  nothing else are **parked** and reported.
- **Log every side effect** with `fiken-cli log-event`: `--surface browser` for confirms
  keyed `linje:<id>`, `--surface api` for purchases, payments and inbox deletions.
- Writes need live mode: `FIKEN_MODE=live` in `~/.config/fiken-cli/env` or `./.env.local`.
  A refusal with exit 8 means test mode; stop and tell the user.

## Run

1. Resolve `bankAccountId` for the account: `fiken-cli bank-accounts get <slug> --agent`.
2. Open the panel for the period and read the lines. Method per surface:
   - Claude in Chrome: [`surfaces/claude-in-chrome.md`](surfaces/claude-in-chrome.md)
   - CDP script against the user's headed Chromium, preferred when port 9222 answers: [`surfaces/cdp.md`](surfaces/cdp.md)
   Both read the same DOM; the shared facts are in
   [`reference/superforing-dom.md`](reference/superforing-dom.md).
3. Pick the flow for each line and follow it to the end:
   - Utbetaling with a supplier receipt: [`flows/utbetaling-kjop.md`](flows/utbetaling-kjop.md)
   - Innbetaling, Vipps- and Stripe-oppgjør, customer invoice payments: [`flows/innbetaling.md`](flows/innbetaling.md)
   - `Antatt` row whose `Leverandør` differs from the bank title: [`flows/leverandor-navnebytte.md`](flows/leverandor-navnebytte.md)
4. Done when every line in the period is either confirmed or parked with a reason, and the
   parked list is in the final report with `linje.id`, date, amount and reason.

## Adding a flow

One file under `flows/`, named after the line kind it handles. It states: how to recognise
the line (label, `linjetype`, sign), the API calls that make it confirmable, the button that
confirms it, and what to log. Add one line to step 3 above. Surfaces stay untouched.
