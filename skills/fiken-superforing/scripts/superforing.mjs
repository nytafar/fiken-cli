#!/usr/bin/env node
// Superføring over Chrome DevTools Protocol. No dependencies: Node >= 22 (global WebSocket).
// Attaches to the user's headed Chromium started with --remote-debugging-port, so the
// human sees every row the agent touches and can sign in when the session expires.
//
//   superforing.mjs list      --slug nyta --account 170218093 --fra 2026-05-01 --til 2026-06-30
//   superforing.mjs buttons   <linjeId>... [same filters]
//   superforing.mjs show      <linjeId>... [--chars 600]   buttons plus the suggestion text of the open row
//   superforing.mjs confirm   <linjeId>... [--label "OK, gå til neste"] [--pick <candidateId>] [--log] [--correlation-id X]
//                             --pick selects the radio candidate first and clicks "Gå til neste" (duplicate-amount rows)
//   superforing.mjs open      [filters]            navigate only
//   superforing.mjs eval      '<js body>'         run page JS with the primitives in scope; `return` a value
//
// Filters: --fra --til --sok --kun-innbetalinger true|false --side N. --port (default 9222).
// Output is TSV on stdout, one row per line. Exit codes: 0 ok, 2 usage, 3 signed out,
// 4 a confirm did not go through (see rows), 5 CDP unreachable.

import { spawnSync } from 'node:child_process';

const argv = process.argv.slice(2);
const cmd = argv.shift();
const opts = {}; const pos = [];
for (let i = 0; i < argv.length; i++) {
  const a = argv[i];
  if (a.startsWith('--')) { const k = a.slice(2); const v = argv[i + 1]; if (v !== undefined && !v.startsWith('--')) { opts[k] = v; i++; } else opts[k] = 'true'; }
  else pos.push(a);
}
const need = (k, d) => opts[k] ?? d ?? die(2, `missing --${k}`);
function die(code, msg) { console.error(msg); process.exit(code); }
if (!['list', 'buttons', 'show', 'confirm', 'open', 'eval'].includes(cmd)) die(2, 'usage: superforing.mjs list|buttons|show|confirm|open [ids...] [--slug --account --fra --til ...]');

const port = opts.port ?? '9222';
const slug = need('slug', 'nyta');
const account = need('account', '170218093');
const q = ['b', 'nf'];
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
  catch (e) { die(5, `no Chromium on port ${port}: ${e.message}`); }
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
async function buttons(id) { if (!(await expand(id))) return null; return [...byId(id).closest('details').querySelectorAll('button')].map(b => norm(b.textContent)).filter(Boolean); }
async function confirm(id, label, pick) {
  if (!(await expand(id))) return { id, ok: false, why: 'missing' };
  if (pick) {
    const d = byId(id).closest('details');
    const r = d.querySelector('input[type=radio][value="' + pick + '"]');
    if (!r) return { id, ok: false, why: 'no-candidate', present: [...d.querySelectorAll('input[type=radio]')].map(x => x.value) };
    r.click(); r.dispatchEvent(new Event('change', { bubbles: true })); await sleep(600);
  }
  const btn = [...byId(id).closest('details').querySelectorAll('button')].find(b => norm(b.textContent) === label);
  if (!btn) return { id, ok: false, why: 'no-button', present: await buttons(id) };
  btn.click();
  for (let i = 0; i < 50; i++) { await sleep(150); if (!byId(id)) return { id, ok: true }; }
  return { id, ok: false, why: 'not-removed' };
}
`;
const wrap = body => `(async () => { ${PRIM} ${body} })()`;

async function ready(cdp) {
  for (let i = 0; i < 60; i++) {
    const s = await cdp.eval(`({ title: document.title, rows: document.querySelectorAll('fk-collapse-title').length, empty: /tomt|ingen (linjer|transaksjoner)/i.test(document.body?.innerText ?? '') })`);
    if (/logg inn/i.test(s.title)) die(3, 'Fiken is signed out. Sign in in the Chromium window, then rerun.');
    if (s.rows > 0 || s.empty) return s;
    await sleep(250);
  }
  die(4, `panel did not render rows within 15 s: ${await cdp.eval("document.title + ' :: ' + (document.body?.innerText ?? '').replace(/\\s+/g,' ').slice(0, 300)")}`);
}

// ---- commands --------------------------------------------------------------------------
const t = await target();
const cdp = await Cdp.connect(t.webSocketDebuggerUrl);
await cdp.send('Page.enable');
await cdp.send('Page.bringToFront');
const strip = u => u.replace(/[&?]linje=\d+/, '').replace(/[&?]continue\b/, '');
const current = await cdp.eval('location.href');
if (strip(current) !== strip(url)) {
  await cdp.send('Page.navigate', { url });
  await sleep(800);
}
await ready(cdp);

const tsv = rows => rows.map(r => r.join('\t')).join('\n');
let exit = 0;

if (cmd === 'open') {
  console.log(url);
} else if (cmd === 'list') {
  const rows = await cdp.eval(wrap(`return linjer().map(l => [l.id, l.dato, l.linjeBelop, l.besteBelop, l.linjetype, norm(l.tittel), norm(l.tekst), (l.folioAlleDokumenter||[]).length, l.erKnyttetTilPosteringsbilag ? 1 : 0]);`));
  console.log('id\tdato\tlinjeBelop\tbesteBelop\tlinjetype\ttittel\ttekst\tdocs\tknyttet');
  if (rows.length) console.log(tsv(rows));
  console.error(`${rows.length} rows  ${url}`);
} else if (cmd === 'buttons') {
  if (!pos.length) die(2, 'buttons needs at least one linje id');
  for (const id of pos) {
    const b = await cdp.eval(wrap(`return await buttons(${Number(id)});`));
    console.log(`${id}\t${b ? b.join(' / ') : '<missing>'}`);
  }
} else if (cmd === 'show') {
  if (!pos.length) die(2, 'show needs at least one linje id');
  const chars = Number(opts.chars ?? 600);
  for (const id of pos) {
    const r = await cdp.eval(wrap(`const b = await buttons(${Number(id)}); if (!b) return null; const d = byId(${Number(id)}).closest('details'); return { b, text: norm(d.innerText).slice(0, ${chars}) };`));
    console.log(`== ${id}\t${r ? r.b.join(' / ') : '<missing>'}\n${r ? r.text : ''}`);
  }
} else if (cmd === 'eval') {
  if (!pos.length) die(2, 'eval needs a JS body');
  const v = await cdp.eval(wrap(pos.join(' ')));
  console.log(typeof v === 'string' ? v : JSON.stringify(v, null, 1));
} else if (cmd === 'confirm') {
  if (!pos.length) die(2, 'confirm needs at least one linje id');
  const label = opts.label ?? (opts.pick ? 'Gå til neste' : 'OK, gå til neste');
  for (const id of pos) {
    const r = await cdp.eval(wrap(`return await confirm(${Number(id)}, ${JSON.stringify(label)}, ${JSON.stringify(opts.pick ?? null)});`));
    let logged = '';
    if (r.ok && opts.log) {
      const args = ['log-event', '--company', slug, '--operation', 'match.confirmed', '--surface', 'browser', '--source-ref', `linje:${id}`, '--agent'];
      if (opts.pick) args.push('--inputs', JSON.stringify({ candidate: opts.pick, button: label }));
      if (opts['correlation-id']) args.push('--correlation-id', opts['correlation-id']);
      const p = spawnSync('fiken-cli', args, { encoding: 'utf8' });
      logged = p.status === 0 ? 'logged' : `log-failed:${(p.stderr || p.stdout).trim().slice(0, 120)}`;
      if (p.status !== 0) exit = 4;
    }
    if (!r.ok) exit = 4;
    console.log([id, r.ok ? 'OK' : 'NO', r.why ?? '', r.present ? r.present.join(' / ') : '', logged].join('\t'));
  }
}
cdp.close();
process.exit(exit);
