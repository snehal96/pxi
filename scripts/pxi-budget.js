#!/usr/bin/env bun
/**
 * scripts/pxi-budget.js: pxi must never cost more memory than the px0 it is
 * built on. Runs both binaries on the same workspace, loads each UI in
 * headless Chrome, and fails when pxi exceeds px0 beyond noise.
 *
 *   bun scripts/pxi-budget.js <px0-binary> <pxi-binary> <workspace> [px0-app.js] [pxi-app.js]
 *
 * Measured: server memory once idle (anonymous RSS / footprint, after its 15 s release), page JS heap after GC, DOM elements idle and after
 * opening pxi's views (Worktrees, Source Control) and returning to Explorer,
 * and the gzipped UI bundle. Budgets are in BUDGET below; see MAINTAINING.md.
 */
import { spawn, execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import zlib from 'node:zlib';

const BUDGET = {
  serverRssPct: 10,      // server memory: at most 10% over px0 ...
  serverRssMB: 1,        // ... or 1 MB, whichever is larger (a ~7 MB Go process varies ±0.3 MB)
  heapPct: 10,           // page JS heap after GC
  idleDomExtra: 120,     // DOM elements pxi adds at rest (the activity bar)
  viewsDomExtra: 200,    // after opening and leaving pxi's views: they must free their rows
  bundleGzWarnPct: 12,   // UI bundle, gzipped: warn above this ...
  bundleGzPct: 25,       // ... and fail above this. Code size is not memory (page JS heap
                         // covers what code costs at run time), but it should stay visible.
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

// Memory the server allocated: anonymous resident memory on Linux, the
// physical footprint on macOS. Plain RSS also counts pages of the program file
// itself, which are shared and reclaimable and grow with binary size, not use.
function rssMB(pid) {
  if (process.platform === 'linux') {
    const m = fs.readFileSync(`/proc/${pid}/status`, 'utf8').match(/^RssAnon:\s+(\d+) kB/m);
    if (m) return Number(m[1]) / 1024;
  }
  if (process.platform === 'darwin') {
    try {
      const out = execFileSync('footprint', ['-p', String(pid), '--noCategories', '-f', 'bytes'], { stderr: 'ignore' }).toString();
      const m = out.match(/Footprint: (\d+)/);
      if (m) return Number(m[1]) / 1048576;
    } catch {}
  }
  return Number(execFileSync('ps', ['-o', 'rss=', '-p', String(pid)]).toString().trim()) / 1024;
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
    // Same editor size for both: px0 opens its right panel on load, pxi does
    // not, and the editor draws only the lines it can show, so a wider editor
    // means more rows. Close it in both so only features are compared.
    await evaluate(`document.body.classList.add('right-hidden'); dispatchEvent(new Event('resize'));`);
    await sleep(800);
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

// pxi: opens Worktrees and Source Control, then returns to Explorer. px0 does
// the same git work through its own "changed files" toggle, so both builds pay
// for px0's git view and only pxi's additions (Worktrees, sections) differ.
const VIEWS = `
  const wait = ms => new Promise(r => setTimeout(r, ms));
  if (document.getElementById('activitybar')) {
    for (const v of ['worktrees', 'scm', 'explorer']) {
      document.querySelector('[data-ab="' + v + '"]')?.click();
      await wait(1500);
    }
  } else {
    await wait(1500);
    document.getElementById('btn-changed')?.click();
    await wait(1500);
    document.getElementById('btn-files')?.click();
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
    // The server returns memory to the OS once it has been idle 15 seconds,
    // checking every 10 (server.go scavenge), so the release lands 15-25 s
    // after the last request. Wait past it: compare settled memory, not
    // garbage that is about to be released.
    await sleep(26000);
    let rss = Infinity;
    for (let i = 0; i < 3; i++) { rss = Math.min(rss, rssMB(server.pid)); await sleep(500); }
    return { label, rss, heap: Math.max(idle.heapMB, views.heapMB), domIdle: idle.dom, domViews: views.dom };
  } finally {
    server.kill();
  }
}

const gz = f => (f && fs.existsSync(f) ? zlib.gzipSync(fs.readFileSync(f)).length / 1024 : null);

// A build's memory varies by a megabyte or more between runs (px0's server
// measured 6.5-8.8 MB on the same CI machine), so each build runs three
// times, alternating, and is judged on its median: one unusual run in either
// direction cannot decide the result.
const RUNS = Number(process.env.PXI_BUDGET_RUNS || 3);
const median = xs => { const s = [...xs].sort((a, b) => a - b); return s[Math.floor(s.length / 2)]; };
const runs = { px0: [], pxi: [] };
for (let i = 0; i < RUNS; i++) {
  runs.px0.push(await measure(px0Bin, 'px0'));
  runs.pxi.push(await measure(pxiBin, 'pxi'));
}
const summarize = rs => ({
  rss: median(rs.map(r => r.rss)), heap: median(rs.map(r => r.heap)),
  domIdle: median(rs.map(r => r.domIdle)), domViews: median(rs.map(r => r.domViews)),
});
const base = summarize(runs.px0);
const mine = summarize(runs.pxi);
console.log('server memory per run (MB): px0 ' + runs.px0.map(r => r.rss.toFixed(2)).join(', ') + ' | pxi ' + runs.pxi.map(r => r.rss.toFixed(2)).join(', '));
const rows = [
  ['server memory (MB)', base.rss, mine.rss, mine.rss <= Math.max(base.rss * (1 + BUDGET.serverRssPct / 100), base.rss + BUDGET.serverRssMB), `≤ +${BUDGET.serverRssPct}%/${BUDGET.serverRssMB}MB`],
  ['page JS heap (MB)', base.heap, mine.heap, mine.heap <= base.heap * (1 + BUDGET.heapPct / 100), `≤ +${BUDGET.heapPct}%`],
  ['DOM elements, idle', base.domIdle, mine.domIdle, mine.domIdle <= base.domIdle + BUDGET.idleDomExtra, `≤ +${BUDGET.idleDomExtra}`],
  ['DOM elements, after views', base.domViews, mine.domViews, mine.domViews <= base.domViews + BUDGET.viewsDomExtra, `≤ +${BUDGET.viewsDomExtra}`],
];
const bgz = gz(px0App), mgz = gz(pxiApp);
if (bgz && mgz) {
  rows.push(['UI bundle gz (KB)', bgz, mgz, mgz <= bgz * (1 + BUDGET.bundleGzPct / 100), `≤ +${BUDGET.bundleGzPct}%`]);
  if (mgz > bgz * (1 + BUDGET.bundleGzWarnPct / 100)) {
    console.log(`::warning::UI bundle is ${((mgz / bgz - 1) * 100).toFixed(1)}% larger than px0's (warning above ${BUDGET.bundleGzWarnPct}%). Consider loading big features on first use.`);
  }
}

let failed = false;
console.log('\nmetric                         px0        pxi     budget    result');
for (const [name, b, m, ok, rule] of rows) {
  failed ||= !ok;
  const f = v => (Number.isInteger(v) ? String(v) : v.toFixed(2)).padStart(9);
  console.log(name.padEnd(27) + f(b) + '  ' + f(m) + '   ' + rule.padEnd(13) + ' ' + (ok ? 'ok' : 'OVER BUDGET'));
}
if (failed) {
  console.error('\npxi uses more memory than px0. Make the new feature free its memory when not in use (see MAINTAINING.md).');
  process.exit(1);
}
console.log('\npxi is within its memory budget.');
