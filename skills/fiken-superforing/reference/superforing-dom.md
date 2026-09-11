# Superføring DOM reference

Shared by every surface. Verified live 2026-09-11 on company `nyta`.

## URL

```
https://fiken.no/foretak/{slug}/bank/bkbf/{bankAccountId}?b&nf&fra=YYYY-MM-DD&til=YYYY-MM-DD
```

| Param | Meaning |
|---|---|
| `b` | bank view |
| `nf` | hide finished lines (`Skjuler ferdige`) |
| `fra`, `til` | period, inclusive |
| `kunInnbetalinger=false` | only utbetalinger |
| `kunInnbetalinger=true` | only innbetalinger |
| `sok=<text>` | substring search on the raw bank text (`tekst`), e.g. `sok=vipps`; `name-cheap` hits, `namecheap` does not |
| `side=N` | page, 0-based; SPA pagination, no reload |
| `linje=<id>` | the one open row; set by clicking a row, advances on confirm |

A page titled `Logg inn - Fiken` means the browser session is signed out; stop and ask the
user to sign in, then navigate again.

`bankAccountId` comes from `fiken-cli bank-accounts get <slug> --agent`. Account picker
without an id: `https://fiken.no/foretak/{slug}/bank/bkbf/`.

## Row model

AngularJS. Each row is `<details>` wrapping a `fk-collapse-title`; the Angular scope on that
title holds `linje`:

| Field | Meaning |
|---|---|
| `id` | stable line id, the natural key for logging and idempotency |
| `dato` | bank date |
| `linjeBelop` | NOK amount as charged, signed |
| `besteBelop` | amount in original currency when the line is foreign, else same as `linjeBelop` |
| `tittel` | cleaned counterparty; `Antatt` badge means Fiken guessed the contact |
| `tekst` | raw bank text, carries currency and rate on card lines |
| `linjetype` | e.g. `overforing`, `ukjent` |
| `erKnyttetTilPosteringsbilag` | already linked to a voucher |
| `folioAlleDokumenter` | inbox documents Fiken matched to the line |

Use `linjeBelop` for payments. `besteBelop` is the currency amount.

Row body and buttons render only while the row is open. Wait about 500 ms after `open`
before reading buttons; reading earlier yields a false "no suggestion".

## Buttons

| Button | Means | Action |
|---|---|---|
| `OK, gå til neste` | Fiken found a booked, paid voucher matching the line | confirm |
| `Bekreft dato` | Vipps-oppgjør with a booked settlement | confirm |
| `Registrer nytt kjøp` / `Registrer nytt salg` | only an inbox document matched, no paid voucher | fix books, reload, re-read |
| `Forslaget stemmer ikke, jeg vil endre` | reject suggestion | never |
| `Fri postering`, `Alternativer` | no suggestion | park |
| `Gå til neste` only, text `Samme beløp finnes flere steder`, radio inputs `Er dette riktig forslag?` | several lines share one amount; each radio `value` is a candidate voucher's transaction id | pick the radio, then `Gå til neste` confirms; with one candidate left there is no radio and `Gå til neste` alone confirms; see [`../flows/dobbeltbetaling.md`](../flows/dobbeltbetaling.md) |

Confirming removes the line from the list (under `nf`), shrinks the count by one, and opens
the next line. The API shows no change on the purchase or its journal entries; the
line-to-voucher link exists only in the panel and in our audit log.

A purchase created via API with a **registered payment** becomes `OK, gå til neste` after a
page reload. Without a payment, the same line keeps `Registrer nytt kjøp` even when the
purchase exists. The panel does not re-evaluate without a reload.

## Reading the list without truncation

The tool that evaluates page JS caps its return at a few hundred characters. Stash the rows on
`window` and page them out:

```js
window.__L = linjer().map(l => [l.id, l.dato, l.linjeBelop, l.besteBelop, norm(l.tittel).slice(0,28)].join('|'));
'N=' + window.__L.length + '\n' + window.__L.slice(0, 16).join('\n')   // then slice(16, 32) …
```

Five fields per row and sixteen rows per call fits. Button reads and confirms return one
short line per id.

## Primitives (page context JS)

```js
const ng = window.angular, norm = s => (s||'').replace(/\s+/g,' ').trim();
const sleep = ms => new Promise(r => setTimeout(r, ms));
const titles = () => [...document.querySelectorAll('fk-collapse-title')];
const linjer = () => titles().map(t => { try { return ng.element(t).scope().linje } catch (e) { return null } }).filter(Boolean);
const byId = id => titles().find(t => { try { return ng.element(t).scope().linje.id === id } catch (e) { return false } });

async function expand(id) {
  let t = byId(id); if (!t) return false;
  if (!t.closest('details').hasAttribute('open')) t.closest('summary').click();
  for (let i = 0; i < 25; i++) { await sleep(120); t = byId(id); if (t && t.closest('details').hasAttribute('open')) break; }
  await sleep(500); return true;
}
async function buttons(id) { await expand(id); return [...byId(id).closest('details').querySelectorAll('button')].map(b => norm(b.textContent)).filter(Boolean); }
async function confirm(id, label = 'OK, gå til neste') {
  await expand(id);
  const btn = [...byId(id).closest('details').querySelectorAll('button')].find(b => norm(b.textContent) === label);
  if (!btn) return { id, ok: false, present: await buttons(id) };
  btn.click();
  for (let i = 0; i < 50; i++) { await sleep(150); if (!byId(id)) return { id, ok: true }; }
  return { id, ok: false, why: 'not-removed' };
}
```

Return compact strings, one line per row, not full objects: `linjer().map(l => [l.id, l.dato,
l.linjeBelop, norm(l.tittel)].join('|')).join('\n')`.

Reading `location.search` or `document.cookie` from the Claude in Chrome tool is blocked;
derive the open row from `details[open] fk-collapse-title` instead.
