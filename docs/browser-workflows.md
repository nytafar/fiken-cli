# Browser workflows — Fiken Superføring (bank reconciliation)

Automation for the parts of Fiken that the REST API does **not** expose. The
ledger itself is API-reachable (and mirrored to SQLite by the CLI), but the
**Superføring** bank-matching engine — where bank-statement lines are matched
to vouchers and confirmed — is browser-only. These workflows drive that UI
through the DOM (Claude-in-Chrome / CDP, or any Playwright/Puppeteer runtime).

> **Guiding principle:** read and act through the **DOM and Angular scope**, not
> screenshots or mouse coordinates. It is ~10× cheaper, deterministic, and
> robust against scroll/zoom/layout. Reserve a screenshot for an occasional
> sanity check only.

---

## 1. The target UI

Page: `https://fiken.no/foretak/{slug}/bank/bkbf/{kontoId}?…&side={n}&linje={id}`
(title: "Superføring").

It is an **AngularJS** app. Key structural facts, all verified live:

| Fact | Detail |
|------|--------|
| Framework | AngularJS (`ng-*` directives, `angular.element(el).scope()` works) |
| A row | native `<details>`/`<summary>` wrapped by a custom `fk-collapse` directive |
| Row title element | `fk-collapse-title`; the bold label is its `.text-bold` child |
| Expand toggle | the `<summary>` ancestor — `ng-click="$ctrl.keydownOrClickHandler($event)"`; native `.click()` triggers it |
| Open state | the row's `<details>` gains the `open` attribute |
| **One row open at a time** | driven by the `&linje=` URL param; opening row B collapses row A |
| **Buttons materialize on expansion** | Angular renders the row body (and its action buttons) only while open |
| Per-row identity | `angular.element(title).scope().linje.id` — equals the `&linje=` value; a stable DB id |
| Confirm side-effect | the line is reconciled, **removed from the list** (the "Skjuler ferdige" filter hides finished lines), and the view **auto-advances** (`&linje=` jumps to the next line) |
| Pagination | Bootstrap `.pagination` control; **SPA** (`ng-click`, no full reload) — so JS state / `window.*` **persists across page changes** |

### The `linje` scope object

Available **without expanding** the row (`angular.element(title).scope().linje`):

```
id, nr, utskriftId, tittel, linjetype, dato, besteDato,
linjeBelop, besteBelop, tekst, plVasket, plVanligLeverandornavn,
motpartInfo, logoUrl, logoDomene, folioAlleDokumenter,
erKnyttetTilPosteringsbilag, ignorert, lest
```

- `id` — the `linje.id` (natural key).
- `besteDato` / `dato` — date; `besteBelop` / `linjeBelop` — amount (kr, signed).
- `tittel` — cleaned counterparty; `tekst` — raw bank text; `linjetype` — e.g. `overforing`, `ukjent`.
- `erKnyttetTilPosteringsbilag` — already linked to a voucher? (false = not yet booked).

---

## 2. Line taxonomy → which button each exposes

A bank line, once expanded, presents **different actions depending on what Fiken
can match**. This is the single most important thing to branch on:

| Line kind | Detection | Confirm button | One-click? |
|-----------|-----------|----------------|:---:|
| **Vipps-oppgjør** (settlement) | label `Overføring av Vipps-oppgjør` | **`Bekreft dato`** | ✅ |
| **Stripe** payout | label `Overføring: FILIAL AF BANKING CIRC` | **`OK, gå til neste`** | ✅ |
| **Antatt — confirm-existing** | `Antatt` badge + proposal *"…trenger ikke gjøre noe…"* | **`OK, gå til neste`** | ✅ |
| **Antatt — create-then-match** | `Antatt` badge + proposal *"Vi fant en kvittering/faktura…"* | `Registrer nytt kjøp` / `Registrer nytt salg` | ❌ (must create voucher) |
| **No suggestion** | only `Alternativer` + `Fri postering` (or `Registrer nytt salg`) | — | ❌ |

Detect an **Antatt** row by a descendant leaf element whose text is exactly
`Antatt`:

```js
const isAntatt = t => [...t.querySelectorAll('*')]
  .some(e => e.children.length === 0 && e.textContent.trim() === 'Antatt');
```

**The fork that drives everything downstream:** presence of a one-click confirm
button (`Bekreft dato` / `OK, gå til neste`) ⇒ *confirm-existing*. Absence
(`Registrer nytt kjøp/salg`) ⇒ *create-then-match* (classify → create
voucher+invoice via API → refresh page → the line then surfaces as confirmable).

---

## 3. Reusable primitives

Drop these into the page context. They are the building blocks for every
workflow below.

```js
const ng    = window.angular;
const sleep = ms => new Promise(r => setTimeout(r, ms));
const norm  = s => (s || '').replace(/\s+/g, ' ').trim();

// --- pagination (Bootstrap .pagination; SPA, no reload) ---
const activePage = () => { const a = document.querySelector('.pagination .active'); return a ? +norm(a.textContent) : null; };
const pageButton = n => [...document.querySelectorAll('.pagination a, .pagination button')].find(e => norm(e.textContent) === String(n));
const lastPage   = () => { const ns = [...document.querySelectorAll('.pagination a, .pagination button')].map(e => +norm(e.textContent)).filter(Number.isFinite); return ns.length ? Math.max(...ns) : 0; };
async function gotoPage(n){
  if (activePage() === n) return true;
  const b = pageButton(n); if (!b) return false; b.click();
  for (let i = 0; i < 45; i++){ await sleep(150); if (activePage() === n){ await sleep(450); return true; } }
  return false;
}

// --- rows (identity = linje.id from Angular scope) ---
const titleById = id => {
  for (const t of document.querySelectorAll('fk-collapse-title')){
    try { const sc = ng.element(t).scope(); if (sc && sc.linje && sc.linje.id === id) return t; } catch(e){}
  }
  return null;
};

// CRITICAL: settle ~450ms after open so the suggestion panel (and its buttons) render
async function expand(id){
  let t = titleById(id); if (!t) return false;
  let d = t.closest('details'); if (d && d.hasAttribute('open')) return true;
  const s = t.closest('summary'); if (!s) return false; s.click();
  for (let i = 0; i < 25; i++){ await sleep(120); t = titleById(id); d = t && t.closest('details'); if (d && d.hasAttribute('open')){ await sleep(450); return true; } }
  return false;
}

async function findAcrossPages(id){
  for (let p = 1; p <= lastPage(); p++){ await gotoPage(p); if (titleById(id)) return p; }
  return null;
}

// scan current page for target rows by label
function scanTargets(){
  const out = [];
  for (const t of document.querySelectorAll('fk-collapse-title')){
    const b = t.querySelector('.text-bold'); const label = norm(b ? b.textContent : t.textContent);
    let id = null; try { const sc = ng.element(t).scope(); if (sc && sc.linje) id = sc.linje.id; } catch(e){}
    if (id == null) continue;
    if (/Vipps-oppgjør/.test(label))                 out.push({ id, type: 'vipps'  });
    else if (/FILIAL AF BANKING CIRC/.test(label))   out.push({ id, type: 'filial' });
  }
  return out;
}

// expand → click exact-text button → verify the row is gone
async function confirm(id, buttonLabel){
  if (!await expand(id)) return { id, ok: false, why: 'expand' };
  const d = titleById(id).closest('details');
  const btn = [...d.querySelectorAll('button')].find(b => norm(b.textContent) === buttonLabel);
  if (!btn) return { id, ok: false, why: 'no-suggestion', present: [...d.querySelectorAll('button')].map(b => norm(b.textContent)).filter(Boolean) };
  btn.click();
  for (let i = 0; i < 50; i++){ await sleep(150); if (!titleById(id)) return { id, ok: true }; }
  return { id, ok: false, why: 'not-removed' };
}
```

---

## 4. Workflows

### 4a. Enumerate / document (read-only)

Sweep every page, classify rows, no side effects. Cheap (one call) because
pagination is SPA. Use it to build a worklist before acting.

```js
const perPage = {};
for (let n = 1; n <= lastPage(); n++){
  await gotoPage(n);
  // collect from scanTargets() + Antatt detection + the linje fields you need
  perPage[n] = scanTargets();
}
```

For an **Antatt worklist** with proposals, expand each Antatt row and read the
proposal from `.cmp-bkbf-forslag-innledning` / `.cmp-bkbf-forslag-beskrivelse`
via **`.innerText`** (not `.textContent` — the body contains an inline `<style>`
block that pollutes `textContent`). Record `confirmBtn` presence to fork
confirm-existing vs create-then-match.

### 4b. Confirm auto-matched lines on one page

```js
const want = { vipps: 'Bekreft dato', filial: 'OK, gå til neste' };
let parked = [];
while (true){
  const targets = scanTargets().filter(t => !parked.includes(t.id));
  if (!targets.length) break;
  const r = targets[0];                       // re-scan each time: list reflows within the page
  const res = await confirm(r.id, want[r.type]);
  if (!res.ok) parked.push(r.id);             // park, don't retry-loop
}
```

**Guardrail:** only ever call `confirm()` with a button label that matches the
row's *type*. After `OK, gå til neste`, Fiken auto-opens the *next* line, which
may be unrelated — never click its button blindly.

### 4c. Pagination sweep (the page-walk)

Confirming hides a line and **reflows** later lines forward; the page count
shrinks. So you cannot pre-compute page assignments. Walk pages, **drain each
before advancing**, and re-read `lastPage()` every loop:

```js
let page = 1;
while (page <= lastPage()){
  if (!await gotoPage(page)){ page++; continue; }
  while (scanTargets().filter(t => !parked.includes(t.id)).length){
    const r = scanTargets().filter(t => !parked.includes(t.id))[0];
    const res = await confirm(r.id, want[r.type]);
    if (!res.ok) parked.push(r.id);           // do NOT advance — reflow pulled new lines onto this page
  }
  page++;                                      // advance only when page is target-free
}
```

Correctness: staying on a page and re-scanning catches lines that reflow in from
behind. Non-target lines stay put and anchor the page; once a page holds only
non-targets it can't gain new targets, so it never needs revisiting.

### 4d. Antatt — two paths

- **Group A (confirm-existing, `OK, gå til neste`)** — gated. Surface to the
  user/classifier first (Fiken *inferred* the contact; the guess can be wrong).
  Confirm exactly like 4b/4c with `confirm(id, 'OK, gå til neste')`.
- **Group B (`Registrer nytt kjøp`/`salg`)** — **not** one-click. Hand to the
  create-voucher pipeline. Note Fiken has usually **already located a candidate
  inbox receipt** (`folioAlleDokumenter`, proposal text), which de-risks
  classification.

---

## 5. Hard-won lessons (read before extending)

1. **45 s CDP timeout.** `Runtime.evaluate` is killed at ~45 s. A full multi-page
   confirm run is minutes. → **Time-budget each call** (`performance.now()`,
   stop at ~36–38 s), **persist progress to `window.__state`**, and re-invoke to
   resume. The loops are naturally resumable because they scan live for what's
   left. (A headless Playwright/Puppeteer runtime has no such cap — see §7.)

2. **Render race → false "no suggestion".** If you read/click buttons
   immediately after opening a row, the Angular suggestion panel may not have
   rendered yet, and you'll wrongly conclude there's no confirm button. → wait
   **~450–550 ms after `open`** before reading buttons. In one run this caused 11
   of 15 "parked" rows; a verify pass with a longer settle recovered all 11.

3. **Drive by `linje.id`, never by coordinates or cached nodes.** Angular
   recompiles DOM nodes (`fk-recompile`, `data-previous`) and rows reflow across
   pages as lines clear. Re-resolve the node from scope on every access.

4. **Always run a verify + final authoritative sweep.** After a confirm run,
   re-scan to (a) catch transient misses, (b) confirm genuine no-suggestion
   lines, (c) get the true remaining count. A row that "clicked but didn't
   vanish" may actually have posted (slow) — re-scan tells you.

5. **Park, don't retry-loop.** A row that won't clear goes on a `parked` list so
   the walk advances; investigate parked rows separately rather than spinning.

---

## 6. Worked result (reference run)

A full reconciliation of one account (2026) over 6 pages:

- **Vipps-oppgjør:** 33 confirmed (`Bekreft dato`), 2 left (no settlement → `Fri postering`).
- **Stripe (FILIAL AF):** 34 confirmed (`OK, gå til neste`), 2 left (`Registrer nytt salg`).
- **Antatt Group A:** 8 confirmed (`OK, gå til neste`) — supplier giro matches (Posten, Scanasia, Fiken).
- **Antatt Group B:** 21 pending — `Registrer nytt kjøp/salg`, receipt pre-located.
- **Total: 75 lines reconciled**, 25 remaining — all of shape *create-then-match*.

Cost: fully DOM-driven, ~a dozen JS calls across the whole session, zero
coordinate clicks; one screenshot used only as a final visual sanity check.

---

## 7. Architecture notes

- **State store.** Bank-statement lines have **no representation in the ledger
  API** — `linje.id` is browser-only. To make the create→confirm pipeline
  crash-safe (avoid duplicate vouchers in the window between "voucher created"
  and "match confirmed"), persist a small `bank_line` table keyed on `linje.id`,
  whose `voucher_id` is a **foreign key into the API-synced ledger mirror** (so
  it owns no financial truth — no divergence). It is an idempotency journal +
  worklist + audit artifact, and a *projection* of the existing append-only log
  table, not a competing source of truth. Keep `(acct, date, amount, currency,
  raw_text)` as a natural-key fallback in case `linje.id` ever rotates.

- **Headless future.** The 45 s cap and render races are artifacts of the
  Chrome-extension CDP bridge. Running the same DOM logic under headless
  Playwright/Puppeteer removes the timeout and gives precise `waitForSelector`
  control — the multi-page walk becomes one clean uninterrupted pass. That is
  the production home for these workflows alongside the CLI.

---

## 8. Button reference

| Norwegian | Meaning | Appears on | Action taken |
|-----------|---------|------------|--------------|
| `Bekreft dato` | Confirm date | Vipps-oppgjør | links payout to booked settlement |
| `OK, gå til neste` | OK, go to next | Stripe, Antatt (confirm-existing) | confirms match, auto-advances |
| `Registrer nytt kjøp` | Register new purchase | Antatt create (outgoing) | opens create-voucher flow |
| `Registrer nytt salg` | Register new sale | Antatt/Stripe create (incoming) | opens create-sale flow |
| `Fri postering` | Free posting | no-suggestion lines | manual posting |
| `Forslaget stemmer ikke, jeg vil endre` | Proposal is wrong, I'll change it | any suggestion row | reject/edit suggestion |
| `Alternativer` | Options | most rows | secondary menu |
