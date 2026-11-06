#!/usr/bin/env bun
/**
 * scripts/pxi-budget.js: pxi must never cost more memory than the px0 it is
 * built on. Runs both binaries on the same workspace, loads each UI in
 * headless Chrome, and fails when pxi exceeds px0 beyond noise.
 *
 *   bun scripts/pxi-budget.js <px0-binary> <pxi-binary> <workspace> [px0-app.js] [pxi-app.js]
 *
 * Measured: server RSS once idle (after its 15 s memory release), page JS heap after GC, DOM elements idle and after
 * opening pxi's views (Worktrees, Source Control) and returning to Explorer,
 * and the gzipped UI bundle. Budgets are in BUDGET below; see MAINTAINING.md.
 */
import { spawn, execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import zlib from 'node:zlib';

const BUDGET = {
  serverRssPct: 10,      // server RSS may be at most 10% over px0 (measurement noise)
  heapPct: 10,           // page JS heap after GC
  idleDomExtra: 120,     // DOM elements pxi adds at rest (the activity bar)
  viewsDomExtra: 200,    // after opening and leaving pxi's views: they must free their rows
  bundleGzPct: 12,       // UI bundle, gzipped
};

const [px0Bin, pxiBin, workspace, px0App, pxiApp] = process.argv.slice(2);
if (!px0Bin || !pxiBin || !workspace) {
  console.error('usage: bun scripts/pxi-budget.js <px0-binary> <pxi-binary> <workspace> [px0-app.js] [pxi-app.js]');
  process.exit(2);
}

const sleep = ms => new Promise(r => setTimeout(r, ms));
const CHROME = process.env.CHROME || [
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  '/usr/bin/google-chrome', '/usr/bin/google-chrome-stable', '/usr/bin/chromium', '/usr/bin/chromium-browser',
].find(p => fs.existsSync(p));
if (!CHROME) { console.error('no Chrome found (set CHROME)'); process.exit(2); }

function rssMB(pid) {
  const kb = Number(execFileSync('ps', ['-o', 'rss=', '-p', String(pid)]).toString().trim());
  return kb / 1024;
}

async function waitFor(url, ms = 30000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    try { if ((await fetch(url)).ok) return; } catch {}
    await sleep(100);
  }
  throw new Error('timed out waiting for ' + url);
}

async function pageMetrics(url, steps) {
  const prof = fs.mkdtempSync(path.join(os.tmpdir(), 'pxi-budget-'));
  const port = 9300 + Math.floor(Math.random() * 500);
  const chrome = spawn(CHROME, ['--headless=new', '--no-sandbox', `--user-data-dir=${prof}`, `--remote-debugging-port=${port}`,
    '--window-size=1440,900', 'about:blank'], { stdio: 'ignore' });
  try {
    let target;
    for (let i = 0; i < 300 && !target; i++) {
      try { target = (await (await fetch(`http://127.0.0.1:${port}/json`)).json()).find(t => t.type === 'page'); } catch {}
      if (!target) await sleep(50);
    }
    const ws = new WebSocket(target.webSocketDebuggerUrl);
    await new Promise(r => { ws.onopen = r; });
    let id = 0; const pending = new Map();
    ws.onmessage = e => { const m = JSON.parse(e.data); if (pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); } };
    const send = (method, params = {}) => new Promise(r => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })); });
    const evaluate = expr => send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true });
    await send('Page.enable');
    await send('Page.navigate', { url });
    await sleep(5000);
    if (steps) { await evaluate(`(async () => { ${steps} })()`); await sleep(3000); }
    await send('HeapProfiler.collectGarbage');
    await sleep(500);
    const heap = await send('Runtime.getHeapUsage');
    const nodes = await evaluate('document.getElementsByTagName("*").length');
    ws.close();
    return { heapMB: heap.result.usedSize / 1048576, dom: nodes.result.result.value };
  } finally {
    chrome.kill();
    fs.rmSync(prof, { recursive: true, force: true });
  }
}

// Opens Worktrees and Source Control, then returns to Explorer. A no-op on px0.
const VIEWS = `
  const wait = ms => new Promise(r => setTimeout(r, ms));
  for (const v of ['worktrees', 'scm', 'explorer']) {
    document.querySelector('[data-ab="' + v + '"]')?.click();
    await wait(1500);
  }`;

async function measure(bin, label) {
  const port = 47900 + Math.floor(Math.random() * 90);
  const server = spawn(bin, ['-no-open', '-no-update', '-no-telemetry', '-host', '127.0.0.1', '-port', String(port), workspace], { stdio: 'ignore' });
  try {
    const url = `http://127.0.0.1:${port}/`;
    await waitFor(url + 'api/meta');
    await sleep(4000); // index built
    const idle = await pageMetrics(url, '');
    const views = await pageMetrics(url, VIEWS);
    // The server returns memory to the OS after 15 seconds without requests
    // (server.go scavenge); compare settled memory, the lowest of 3 samples.
    await sleep(17000);
    let rss = Infinity;
    for (let i = 0; i < 3; i++) { rss = Math.min(rss, rssMB(server.pid)); await sleep(500); }
    return { label, rss, heap: Math.max(idle.heapMB, views.heapMB), domIdle: idle.dom, domViews: views.dom };
  } finally {
    server.kill();
  }
}

const gz = f => (f && fs.existsSync(f) ? zlib.gzipSync(fs.readFileSync(f)).length / 1024 : null);

const base = await measure(px0Bin, 'px0');
const mine = await measure(pxiBin, 'pxi');
const rows = [
  ['server RSS (MB)', base.rss, mine.rss, mine.rss <= base.rss * (1 + BUDGET.serverRssPct / 100), `≤ +${BUDGET.serverRssPct}%`],
  ['page JS heap (MB)', base.heap, mine.heap, mine.heap <= base.heap * (1 + BUDGET.heapPct / 100), `≤ +${BUDGET.heapPct}%`],
  ['DOM elements, idle', base.domIdle, mine.domIdle, mine.domIdle <= base.domIdle + BUDGET.idleDomExtra, `≤ +${BUDGET.idleDomExtra}`],
  ['DOM elements, after views', base.domViews, mine.domViews, mine.domViews <= base.domViews + BUDGET.viewsDomExtra, `≤ +${BUDGET.viewsDomExtra}`],
];
const bgz = gz(px0App), mgz = gz(pxiApp);
if (bgz && mgz) rows.push(['UI bundle gz (KB)', bgz, mgz, mgz <= bgz * (1 + BUDGET.bundleGzPct / 100), `≤ +${BUDGET.bundleGzPct}%`]);

let failed = false;
console.log('\nmetric                         px0        pxi     budget    result');
for (const [name, b, m, ok, rule] of rows) {
  failed ||= !ok;
  const f = v => (Number.isInteger(v) ? String(v) : v.toFixed(2)).padStart(9);
  console.log(name.padEnd(27) + f(b) + '  ' + f(m) + '   ' + rule.padEnd(9) + ' ' + (ok ? 'ok' : 'OVER BUDGET'));
}
if (failed) {
  console.error('\npxi uses more memory than px0. Make the new feature free its memory when not in use (see MAINTAINING.md).');
  process.exit(1);
}
console.log('\npxi is within its memory budget.');
