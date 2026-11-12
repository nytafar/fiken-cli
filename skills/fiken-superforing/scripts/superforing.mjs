#!/usr/bin/env node
// Superføring over Chrome DevTools Protocol. No dependencies: Node >= 22 (global WebSocket).
// Attaches to a headed Chrome/Chromium with a DevTools port (see surfaces/cdp.md), so the
// human sees every row the agent touches and can sign in when the session expires.
//
//   superforing.mjs probe     [--port N]          what answers on the port, and its Fiken tabs; navigates nowhere
//   superforing.mjs list      --slug nyta --account 170218093 --fra 2026-05-01 --til 2026-06-30 [--buttons]
//   superforing.mjs buttons   <linjeId>... [same filters]
//   superforing.mjs show      <linjeId>... [--chars 600]   buttons plus the suggestion text of the open row
//   superforing.mjs confirm   <linjeId>... [--label "OK, gå til neste"] [--pick <candidateId>] [--log] [--correlation-id X]
//                             --pick selects the radio candidate first and clicks "Gå til neste" (duplicate-amount rows)
//                             reloads the panel first, so API writes since the last load are seen (--no-reload skips)
//   superforing.mjs book      <linjeId> --purchase <body.json> --file <receipt.pdf> [--filename N] [--doc <inboxDocumentId>]
//                             [--correlation-id X] [--dry-run]   create paid purchase -> attach -> drop inbox doc -> reload -> confirm, all logged
//   superforing.mjs open      [filters]            navigate only
//   superforing.mjs eval      '<js body>'         run page JS with the primitives in scope; `return` a value
//
// Filters: --fra --til --sok --kun-innbetalinger true|false --side N --all (include finished lines). --port (default 9222).
// --reload forces a page reload on any command. Output is TSV on stdout, one row per line.
// Exit codes: 0 ok, 2 usage, 3 signed out, 4 a confirm or book step did not go through (see rows),
// 5 no browser on the port, or something other than a browser.

import { spawnSync } from 'node:child_process';
import { readFileSync, existsSync } from 'node:fs';
import { basename } from 'node:path';

const argv = process.argv.slice(2);
const cmd = argv.shift();
const opts = {}; const pos = [];
const BOOL = new Set(['all', 'log', 'reload', 'no-reload', 'buttons', 'dry-run']);
for (let i = 0; i < argv.length; i++) {
  const a = argv[i];
  if (a.startsWith('--')) { const k = a.slice(2); const v = argv[i + 1]; if (!BOOL.has(k) && v !== undefined && !v.startsWith('--')) { opts[k] = v; i++; } else opts[k] = 'true'; }
  else pos.push(a);
}
const need = (k, d) => opts[k] ?? d ?? die(2, `missing --${k}`);
function die(code, msg) { console.error(msg); process.exit(code); }
if (!['probe', 'list', 'buttons', 'show', 'confirm', 'book', 'open', 'eval'].includes(cmd)) die(2, 'usage: superforing.mjs probe|list|buttons|show|confirm|book|open|eval [ids...] [--slug --account --fra --til ...]');

const port = opts.port ?? '9222';
const slug = need('slug', 'nyta');
const account = need('account', '170218093');
const q = opts.all ? ['b'] : ['b', 'nf'];
if (opts.fra) q.push(`fra=${opts.fra}`);
if (opts.til) q.push(`til=${opts.til}`);
if (opts.sok) q.push(`sok=${encodeURIComponent(opts.sok)}`);
if (opts['kun-innbetalinger']) q.push(`kunInnbetalinger=${opts['kun-innbetalinger']}`);
if (opts.side) q.push(`side=${opts.side}`);
const panelPath = `/foretak/${slug}/bank/bkbf/${account}`;
const url = `https://fiken.no${panelPath}?${q.join('&')}`;

// ---- CDP plumbing ---------------------------------------------------------------------
async function http(path, method = 'GET') {
  try { const r = await fetch(`http://127.0.0.1:${port}${path}`, { method }); return await r.json(); }
  catch (e) { die(5, `nothing answers on port ${port} (${e.message}). Start or connect a browser: surfaces/cdp.md`); }
}
// The port is only a number: Electron apps (Obsidian, VS Code) expose the same protocol on 9222.
async function browser() {
  const v = await http('/json/version');
  const ua = v['User-Agent'] ?? '';
  if (/Electron/i.test(ua)) die(5, `port ${port} is an Electron app, not a browser (${ua.match(/\b[\w-]+\/[\d.]+ Chrome/)?.[0]?.replace(' Chrome', '') ?? 'unknown app'}). Use another --port: surfaces/cdp.md`);
  if (/Headless/i.test(v.Browser ?? '')) console.error(`warning: ${v.Browser} is headless; nobody can sign in or watch`);
  return v;
}
async function target() {
  const pages = (await http('/json')).filter(t => t.type === 'page');
  let t = pages.find(p => p.url.includes(panelPath));
  if (!t) t = await http(`/json/new?${encodeURIComponent(url)}`, 'PUT');
  return t;
}
class Cdp {
  constructor(ws) { this.ws = ws; this.n = 0; this.pending = new Map(); this.events = []; ws.onmessage = m => this.onmsg(JSON.parse(m.data)); }
  static async connect(wsUrl) { const ws = new WebSocket(wsUrl); await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; }); return new Cdp(ws); }
  onmsg(m) { if (m.id && this.pending.has(m.id)) { const { res, rej } = this.pending.get(m.id); this.pending.delete(m.id); m.error ? rej(new Error(m.error.message)) : res(m.result); } else if (m.method) this.events.push(m); }
  send(method, params = {}) { const id = ++this.n; this.ws.send(JSON.stringify({ id, method, params })); return new Promise((res, rej) => this.pending.set(id, { res, rej })); }
  async eval(expr) { const r = await this.send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true }); if (r.exceptionDetails) throw new Error(r.exceptionDetails.text + ' ' + (r.exceptionDetails.exception?.description ?? '')); return r.result.value; }
  close() { this.ws.close(); }
}
const sleep = ms => new Promise(r => setTimeout(r, ms));

// ---- page-context primitives (see reference/superforing-dom.md) ---------------------------
const PRIM = `
const ng = window.angular, norm = s => (s||'').replace(/\\s+/g,' ').trim();
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
// The suggestion renders after the row opens, at its own pace. Done when two reads 250 ms
// apart agree on a non-empty button list; [] after 5 s means "never rendered", not "no suggestion".
async function buttons(id) {
  if (!(await expand(id))) return null;
  let prev = null;
  for (let i = 0; i < 20; i++) {
    const d = byId(id)?.closest('details');
    const key = d ? [...d.querySelectorAll('button')].map(b => norm(b.textContent)).filter(Boolean).join('\u0001') : '';
    if (key && key === prev) return key.split('\u0001');
    prev = key; await sleep(250);
  }
  return [];
}
async function confirm(id, label, pick) {
  if (!(await expand(id))) return { id, ok: false, why: 'missing' };
  if (pick) {
    const d = byId(id).closest('details');
    const r = d.querySelector('input[type=radio][value="' + pick + '"]');
    if (!r) return { id, ok: false, why: 'no-candidate', present: [...d.querySelectorAll('input[type=radio]')].map(x => x.value) };
    r.click(); r.dispatchEvent(new Event('change', { bubbles: true })); await sleep(600);
  }
  const present = await buttons(id);
  const btn = [...byId(id).closest('details').querySelectorAll('button')].find(b => norm(b.textContent) === label);
  if (!btn) return { id, ok: false, why: 'no-button', present };
  btn.click();
  for (let i = 0; i < 50; i++) { await sleep(150); if (!byId(id)) return { id, ok: true }; }
  return { id, ok: false, why: 'not-removed' };
}
`;
const wrap = body => `(async () => { ${PRIM} ${body} })()`;

async function ready(cdp) {
  for (let i = 0; i < 60; i++) {
    if (i === 16) { const done = await cdp.eval("document.readyState === 'complete' && !document.querySelector('.ladda-spinner, .spinner, [class*=loading]')"); if (done) return { rows: 0, empty: true }; }
    const s = await cdp.eval(`({ title: document.title, rows: document.querySelectorAll('fk-collapse-title').length, empty: /tomt|ingen (linjer|transaksjoner)/i.test(document.body?.innerText ?? '') })`);
    if (/logg inn/i.test(s.title)) die(3, 'Fiken is signed out. Sign in in the Chromium window, then rerun.');
    if (s.rows > 0 || s.empty) return s;
    await sleep(250);
  }
  die(4, `panel did not render rows within 15 s: ${await cdp.eval("document.title + ' :: ' + (document.body?.innerText ?? '').replace(/\\s+/g,' ').slice(0, 300)")}`);
}

// ---- commands --------------------------------------------------------------------------
const version = await browser();
if (cmd === 'probe') {
  const tabs = (await http('/json')).filter(p => p.type === 'page' && /(^|\.)fiken\.no\//.test(p.url.replace(/^https?:\/\//, '')));
  console.log(`browser\t${version.Browser}`);
  for (const p of tabs) console.log(`tab\t${/logg inn/i.test(p.title) ? 'signed-out' : 'open'}\t${p.url}`);
  if (!tabs.length) console.log('tab\tnone\ta command will open the panel; sign in there if asked');
  process.exit(0);
}
const t = await target();
const cdp = await Cdp.connect(t.webSocketDebuggerUrl);
await cdp.send('Page.enable');
await cdp.send('Page.bringToFront');
const strip = u => u.replace(/[&?]linje=\d+/, '').replace(/[&?]continue\b/, '');
const current = await cdp.eval('location.href');
// Navigating to the URL the tab already shows is not a reload, and the panel never
// re-evaluates a suggestion on its own: after an API write only a reload shows the new state.
async function reload() {
  await cdp.eval('window.__sfStale = true');
  await cdp.send('Page.reload');
  for (let i = 0; i < 60; i++) { await sleep(250); if (!(await cdp.eval('window.__sfStale === true').catch(() => true))) break; }
  await ready(cdp);
}
const wantReload = opts.reload || ((cmd === 'confirm' || cmd === 'book') && !opts['no-reload']);
if (strip(current) !== strip(url)) {
  await cdp.send('Page.navigate', { url });
  await sleep(800);
  await ready(cdp);
} else if (wantReload) await reload();
else await ready(cdp);

const tsv = rows => rows.map(r => r.join('\t')).join('\n');
let exit = 0;

if (cmd === 'open') {
  console.log(url);
} else if (cmd === 'list') {
  const rows = await cdp.eval(wrap(`return linjer().map(l => [l.id, l.dato, l.linjeBelop, l.besteBelop, l.linjetype, norm(l.tittel), norm(l.tekst), (l.folioAlleDokumenter||[]).length, l.erKnyttetTilPosteringsbilag ? 1 : 0]);`));
  if (opts.buttons) for (const r of rows) { const b = await cdp.eval(wrap(`return await buttons(${Number(r[0])});`)); r.push(b?.length ? b.join(' / ') : '<not rendered>'); }
  console.log('id\tdato\tlinjeBelop\tbesteBelop\tlinjetype\ttittel\ttekst\tdocs\tknyttet' + (opts.buttons ? '\tknapper' : ''));
  if (rows.length) console.log(tsv(rows));
  console.error(`${rows.length} rows  ${url}`);
} else if (cmd === 'buttons') {
  if (!pos.length) die(2, 'buttons needs at least one linje id');
  for (const id of pos) {
    const b = await cdp.eval(wrap(`return await buttons(${Number(id)});`));
    console.log(`${id}\t${b ? (b.length ? b.join(' / ') : '<not rendered>') : '<missing>'}`);
  }
} else if (cmd === 'show') {
  if (!pos.length) die(2, 'show needs at least one linje id');
  const chars = Number(opts.chars ?? 600);
  for (const id of pos) {
    const r = await cdp.eval(wrap(`const b = await buttons(${Number(id)}); if (!b) return null; const d = byId(${Number(id)}).closest('details'); return { b, text: norm(d.innerText).slice(0, ${chars}) };`));
    console.log(`== ${id}\t${r ? (r.b.length ? r.b.join(' / ') : '<not rendered>') : '<missing>'}\n${r ? r.text : ''}`);
  }
} else if (cmd === 'eval') {
  if (!pos.length) die(2, 'eval needs a JS body');
  const v = await cdp.eval(wrap(pos.join(' ')));
  console.log(typeof v === 'string' ? v : JSON.stringify(v, null, 1));
} else if (cmd === 'confirm') {
  if (!pos.length) die(2, 'confirm needs at least one linje id');
  const label = opts.label ?? (opts.pick ? 'Gå til neste' : 'OK, gå til neste');
  for (const id of pos) await confirmLine(id, label);
} else if (cmd === 'book') {
  await book();
}

function logEvent(operation, extra) {
  const args = ['log-event', '--company', slug, '--operation', operation, '--agent', ...extra];
  if (opts['correlation-id']) args.push('--correlation-id', opts['correlation-id']);
  const p = spawnSync('fiken-cli', args, { encoding: 'utf8' });
  if (p.status !== 0) exit = 4;
  return p.status === 0 ? 'logged' : `log-failed:${(p.stderr || p.stdout).trim().slice(0, 120)}`;
}
async function confirmLine(id, label) {
  const r = await cdp.eval(wrap(`return await confirm(${Number(id)}, ${JSON.stringify(label)}, ${JSON.stringify(opts.pick ?? null)});`));
  let logged = '';
  if (r.ok && opts.log) {
    const extra = ['--surface', 'browser', '--source-ref', `linje:${id}`];
    if (opts.pick) extra.push('--inputs', JSON.stringify({ candidate: opts.pick, button: label }));
    logged = logEvent('match.confirmed', extra);
  }
  if (!r.ok) exit = 4;
  console.log([id, r.ok ? 'OK' : 'NO', r.why ?? '', r.present ? r.present.join(' / ') : '', logged].join('\t'));
  return r.ok;
}

// book: one open utbetaling line -> paid purchase with its receipt -> confirmed line.
// Every step is checked before the next; a failure stops with exit 4 and says where,
// and a rerun is stopped by the duplicate guard instead of creating a second purchase.
function fiken(args, input) {
  const p = spawnSync('fiken-cli', [...args, '--agent'], { encoding: 'utf8', input, maxBuffer: 64 << 20 });
  let json = null; try { json = JSON.parse(p.stdout); } catch { /* non-JSON output */ }
  return { ok: p.status === 0, json, err: (p.stderr || p.stdout || '').trim().slice(0, 300) };
}
function purchasesBetween(from, to) {
  const r = fiken(['purchases', 'get', slug, '--date-ge', from, '--date-le', to, '--all', '--no-cache', '--data-source', 'live']);
  if (!r.ok || !r.json) die(4, `book: cannot read purchases ${from}..${to}: ${r.err}`);
  return Array.isArray(r.json) ? r.json : (r.json.results ?? []);
}
function shiftDay(d, n) { return new Date(Date.parse(d) + n * 864e5).toISOString().slice(0, 10); }
async function book() {
  if (pos.length !== 1) die(2, 'book takes exactly one linje id');
  const id = Number(pos[0]);
  const file = need('file'); if (!existsSync(file)) die(2, `book: no such file ${file}`);
  const body = JSON.parse(readFileSync(need('purchase'), 'utf8'));
  const linje = await cdp.eval(wrap(`const l = linjer().find(l => l.id === ${id}); return l ? { dato: l.dato, belop: l.linjeBelop } : null;`));
  if (!linje) die(4, `book: linje ${id} is not open in ${url}`);
  const ore = Math.round(-linje.belop * 100);
  const sum = (body.lines ?? []).reduce((a, l) => a + (l.netPrice ?? 0) + (l.vat ?? 0), 0);
  const bad = [];
  if (ore <= 0) bad.push('line is not an utbetaling');
  if (body.currency !== 'NOK') bad.push(`currency ${body.currency}, want NOK`);
  if (body.paid !== true || body.paymentDate !== linje.dato) bad.push(`paid/paymentDate ${body.paid}/${body.paymentDate}, want true/${linje.dato}`);
  if (sum !== ore) bad.push(`lines sum ${sum} øre, line is ${ore} øre`);
  if (!body.identifier) bad.push('no identifier (invoice number)');
  if (bad.length) die(2, `book: purchase does not match linje ${id}: ${bad.join('; ')}`);

  const same = p => p.identifier === body.identifier && (p.supplier?.contactId ?? null) === (body.supplierId ?? null);
  const dup = purchasesBetween(shiftDay(linje.dato, -92), shiftDay(linje.dato, 7)).filter(same);
  if (dup.length) die(4, `book: ${body.identifier} is already purchase ${dup.map(p => `${p.purchaseId} (date ${p.date}, paid ${p.paid})`).join(', ')}. Paid: reload and confirm. Unpaid: register the payment.`);

  const filename = opts.filename ?? basename(file);
  if (opts['dry-run']) { console.log(['dry-run', id, linje.dato, ore, body.identifier, filename, opts.doc ? `inbox:${opts.doc}` : 'no-inbox-doc'].join('\t')); return; }

  const stop = (step, why) => { console.log([id, 'NO', step, why].join('\t')); exit = 4; };
  const created = fiken(['purchases', 'create', slug, '--stdin'], JSON.stringify(body));
  if (!created.ok) return stop('create', created.err);
  const found = purchasesBetween(body.date, body.date).filter(same);
  if (found.length !== 1) return stop('lookup', `${found.length} purchases carry ${body.identifier} on ${body.date}`);
  const pid = String(found[0].purchaseId);
  const src = opts.doc ? `inbox:${opts.doc}` : `linje:${id}`;
  logEvent('purchase.created', ['--surface', 'api', '--target-type', 'purchase', '--target-id', pid, '--source-ref', src]);

  const att = fiken(['purchases', 'attachments', 'add-to-purchase', slug, pid, '--file', file, '--filename', filename, '--attach-to-sale']);
  const n = (purchasesBetween(body.date, body.date).find(p => String(p.purchaseId) === pid)?.purchaseAttachments ?? []).length;
  if (!att.ok || n < 1) return stop('attach', `purchase ${pid} has ${n} attachments: ${att.err}`);

  if (opts.doc) {
    const del = fiken(['inbox', 'delete-document', slug, String(opts.doc)]);
    if (!del.ok) return stop('inbox-delete', `purchase ${pid} is booked with its receipt; inbox ${opts.doc}: ${del.err}`);
    logEvent('inbox.deleted', ['--surface', 'api', '--source-ref', `inbox:${opts.doc}`]);
  }
  await reload();
  opts.log = 'true';
  process.stdout.write(`purchase\t${pid}\t`);
  await confirmLine(id, 'OK, gå til neste');
}
cdp.close();
process.exit(exit);
