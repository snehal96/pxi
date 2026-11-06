// web/src/pxi-claude.js: the Claude tab's loader (fork_claude.go is the server).
//
// The Claude panel is an editor tab next to the file tabs. Its code and styles
// (web/pxi/claude.js, claude.css) load the first time it opens; closing the tab
// removes its DOM and stream (Rule zero). Running sessions keep going without
// it, and while any runs this loader keeps one status stream open for the
// badge that says a session is waiting for you.

import { $, S, esc, doc_, api, apiPostJson, MOD } from './state.js';
import { on } from './bus.js';
import { openFile } from './tabs.js';
import { thrMd } from './thread.js';
import { showToast } from './ui.js';
import { getSelectedRangeInfo, SEL_MENU_ITEMS } from './selbar.js';

const CLAUDE_ICON = '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><path d="M12 3v18M3 12h18M5.6 5.6l12.8 12.8M18.4 5.6L5.6 18.4"/></svg>';

let clTab = null;        // the tab element in the tab row
let clPane = null;       // #pxi-claude, inside #editor
let clActive = false;
let clMod = null;        // the loaded panel (window.pxiClaudePanel)
let clLoading = null;
let clWatch = null;      // status stream while sessions run
let clRuns = [];
let clLastSel = null;    // the editor selection before the Claude tab took focus
let clPending = null;    // open(...) options waiting for the module

function clLoad() {
  if (clLoading) return clLoading;
  const add = (tag, attrs) => new Promise((ok, fail) => {
    const el = document.createElement(tag);
    Object.assign(el, attrs);
    el.onload = ok;
    el.onerror = () => fail(new Error('could not load ' + (attrs.src || attrs.href)));
    document.head.append(el);
  });
  clLoading = add('link', { rel: 'stylesheet', href: 'static/pxi/claude.css' })
    .then(() => add('script', { src: 'static/pxi/claude.js' }))
    .then(() => window.pxiClaudePanel);
  clLoading.catch(() => { clLoading = null; });
  return clLoading;
}

// Paths: the panel speaks absolute paths to Claude; pxi opens workspace paths.
function clRoot() { return S.meta?.root || ''; }

const clCtx = {
  api, apiPostJson, esc, md: thrMd, toast: showToast, S,
  root: clRoot,
  openPath(p, line) { return openFile(p, line ? { line } : {}); },
  // The file and selection the user was looking at, for "send with context".
  context() {
    const d = doc_();
    const sel = clLastSel && d && clLastSel.path === d.path ? clLastSel : null;
    return d && !d.isImage ? { path: d.path, sel } : null;
  },
  setBadge: clSetBadge,
  newWorktree: () => newWorktreeDialog(),
  watch: clStartWatch,
  close: clClose,
};

let clIde = 0;            // terminal claudes connected through the IDE bridge
let clIdeAsking = {};    // ide session -> pending diffs, to notice new ones

function clStartWatch() {
  if (clWatch) return;
  clWatch = new EventSource('api/pxi/claude/watch');
  clWatch.onmessage = e => {
    let j = {};
    try { j = JSON.parse(e.data); } catch {}
    clRuns = j.runs || [];
    clIde = j.ide || 0;
    clMenuItems();
    const asking = clRuns.reduce((n, r) => n + (r.asking || 0), 0);
    clSetBadge(asking);
    clMod?.runs?.(clRuns);
    // A terminal claude proposed an edit: show it, like an editor opening a diff.
    const now = {};
    for (const r of clRuns) {
      if (!r.session.startsWith('ide-')) continue;
      now[r.session] = r.asking || 0;
      if ((r.asking || 0) > (clIdeAsking[r.session] || 0)) openClaude({ session: r.session, where: '@terminal', quiet: true });
    }
    clIdeAsking = now;
    clNotify(clRuns, asking);
    for (const req of j.page || []) clPageRequest(req);
    if (clIde) clSendContext();
    // Nothing running, no terminal claude and no tab: stop listening.
    if (!clRuns.length && !clTab && !clIde && !clTermShown) { clWatch.close(); clWatch = null; }
  };
}

// ------------------------------------------------------------ notifications
// A session that starts waiting for the user, or finishes, while the user is
// not looking at it: a Mac notification (the app) and the Dock badge. In a
// browser, the waiting count leads the tab title.
let clPrev = null; // session -> { busy, asking } from the previous status
const clBaseTitle = () => document.title.replace(/^\(\d+\) /, '');

function clLooking(session) {
  return document.hasFocus() && clActive && clMod?.current?.() === session;
}

function clNotify(runs, asking) {
  const bridge = window.webkit?.messageHandlers?.pxi;
  if (bridge) bridge.postMessage({ type: 'waiting', count: asking });
  else document.title = (asking ? '(' + asking + ') ' : '') + clBaseTitle();
  const next = {};
  for (const r of runs) {
    next[r.session] = { busy: !!r.busy, asking: r.asking || 0 };
    if (!clPrev) continue; // the first status after load: nothing new happened
    const p = clPrev[r.session] || { busy: false, asking: 0 };
    let title = '';
    if ((r.asking || 0) > p.asking) title = 'Claude needs you';
    else if (p.busy && !r.busy && !r.asking) title = 'Claude finished';
    if (!title || clLooking(r.session)) continue;
    const where = r.where === '@terminal' ? 'terminal' : (r.where || S.meta?.name || 'workspace');
    const body = (r.title || 'Session') + ' · ' + where;
    if (bridge) bridge.postMessage({ type: 'notify', title, body, session: r.session, where: r.where || '' });
  }
  clPrev = next;
}

// The file and selection, for terminal claudes (only while one is connected).
let clCtxTimer = 0, clCtxSig = '';
function clSendContext() {
  clearTimeout(clCtxTimer);
  clCtxTimer = setTimeout(() => {
    const d = doc_();
    if (!clIde || !d || d.isImage) return;
    const sel = clLastSel && clLastSel.path === d.path ? clLastSel : null;
    const body = { path: d.path, text: sel ? sel.text : '', l1: sel ? sel.l1 : d.cur || 1, l2: sel ? sel.l2 : d.cur || 1 };
    const sig = JSON.stringify(body);
    if (sig === clCtxSig) return;
    clCtxSig = sig;
    apiPostJson('/api/pxi/ide/context', body).catch(() => {});
  }, 250);
}

// openFile from a terminal claude: an absolute path, maybe in a worktree.
async function clPageRequest(req) {
  if (!req.open) return;
  const abs = req.open;
  let p = null;
  const root = clRoot();
  if (root && abs.startsWith(root + '/')) p = abs.slice(root.length + 1);
  if (!p) {
    try {
      const wt = await api('/api/worktrees');
      for (const w of wt.worktrees || []) if (abs.startsWith(w.path + '/')) p = '@wt/' + w.name + '/' + abs.slice(w.path.length + 1);
    } catch {}
  }
  if (p) { clHide(); openFile(p); }
}

let clTermShown = false; // the terminal panel is open: a claude may connect any moment

// The terminal panel opened (on) or closed (off).
export function claudeWatch(on = true) {
  clTermShown = on;
  if (on) clStartWatch();
}

function clSetBadge(asking) {
  const b = document.querySelector('#activitybar [data-ab="claude"] .ab-badge');
  if (b) { b.hidden = !asking; b.textContent = asking ? String(asking) : ''; }
  clTab?.classList.toggle('cl-asking', !!asking);
}

function clEnsureTab() {
  if (clTab) return;
  const tabs = $('#tabs');
  if (!tabs) return;
  let row = $('#pxi-tabrow');
  if (!row) {
    row = document.createElement('div');
    row.id = 'pxi-tabrow';
    tabs.before(row);
    row.append(tabs);
  }
  clTab = document.createElement('div');
  clTab.id = 'pxi-claude-tab';
  clTab.className = 'tab pxi-claude-tab';
  clTab.title = 'Claude (' + (MOD === 'metaKey' ? '⌘' : 'Ctrl+') + 'Esc)';
  clTab.innerHTML = '<span class="cl-tab-icon">' + CLAUDE_ICON + '</span><span class="tn">Claude</span>' +
    '<span class="cl-tab-dot" title="Waiting for you"></span>' +
    '<span class="x" title="Close Claude"><svg viewBox="0 0 10 10" aria-hidden="true"><path d="M2 2l6 6M8 2l-6 6"/></svg></span>';
  clTab.addEventListener('mousedown', e => { if (e.button === 1) { e.preventDefault(); clClose(); } });
  clTab.addEventListener('click', e => {
    if (e.target.closest('.x')) { e.stopPropagation(); clClose(); return; }
    clShow();
  });
  row.prepend(clTab);
}

function clShow(quiet) {
  if (!clActive) {
    const info = getSelectedRangeInfo();
    if (info && info.text) clLastSel = { path: info.path, l1: info.l1, l2: info.l2, text: info.text.slice(0, 20000) };
  }
  clEnsureTab();
  if (!clPane) {
    clPane = document.createElement('div');
    clPane.id = 'pxi-claude';
    clPane.innerHTML = '<div class="cl-loading">Loading Claude…</div>';
    $('#editor')?.append(clPane);
  }
  clActive = true;
  document.body.classList.add('pxi-claude-on');
  clTab.classList.add('active');
  clPane.hidden = false;
  clLoad().then(mod => {
    if (!clPane) return;
    if (!clMod) {
      clMod = mod;
      clMod.mount(clPane, clCtx);
      clStartWatch();
    }
    if (clPending) { clMod.open(clPending); clPending = null; }
    if (!quiet) clMod.focus?.();
  }).catch(err => {
    if (clPane) clPane.innerHTML = '<div class="cl-loading">' + esc(err.message) + '</div>';
  });
}

function clHide() {
  if (!clActive) return;
  clActive = false;
  document.body.classList.remove('pxi-claude-on');
  clTab?.classList.remove('active');
  if (clPane) clPane.hidden = true;
}

function clClose() {
  clHide();
  clMod?.unmount();
  clMod = null;
  clPane?.remove();
  clPane = null;
  clTab?.remove();
  clTab = null;
  if (clWatch && !clRuns.length && !clIde && !clTermShown) { clWatch.close(); clWatch = null; }
}

// Opens the Claude tab; opts: { where, session, text } (where: worktree name).
export function openClaude(opts) {
  if (opts) clPending = opts;
  clShow(opts && opts.quiet);
  if (clMod && clPending) { clMod.open(clPending); clPending = null; }
}

export function toggleClaude() {
  if (clActive) clHide(); else clShow();
}

// ------------------------------------------------------------ new worktree + Claude
// A branch and worktree (under .claude/worktrees/) with a Claude session in it,
// started on the task right away when one is given. The dialog exists only
// while open.

export function newWorktreeDialog() {
  if ($('#pxi-newwt')) return;
  const box = document.createElement('div');
  box.id = 'pxi-newwt';
  box.innerHTML = '<form class="nw-card"><div class="nw-title">New worktree with Claude</div>' +
    '<label>Branch<input name="branch" placeholder="feat/login-rate-limit" autocomplete="off" spellcheck="false" required></label>' +
    '<label>From<input name="base" placeholder="default branch" autocomplete="off" spellcheck="false"></label>' +
    '<label>Task for Claude <span>optional; sent as the first message</span><textarea name="task" rows="5" placeholder="Paste the ticket or describe the change…"></textarea></label>' +
    '<div class="nw-err" hidden></div>' +
    '<div class="nw-bar"><button type="button" class="nw-cancel">Cancel</button><button type="submit" class="nw-ok">Create and start Claude</button></div></form>';
  document.body.append(box);
  const form = box.querySelector('form'), err = box.querySelector('.nw-err');
  const close = () => box.remove();
  box.addEventListener('mousedown', e => { if (e.target === box) close(); });
  box.querySelector('.nw-cancel').addEventListener('click', close);
  form.addEventListener('keydown', e => {
    if (e.key === 'Escape') { e.preventDefault(); close(); }
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); form.requestSubmit(); }
  });
  form.addEventListener('submit', async e => {
    e.preventDefault();
    const fd = new FormData(form);
    const ok = form.querySelector('.nw-ok');
    ok.disabled = true; ok.textContent = 'Creating…'; err.hidden = true;
    try {
      const j = await apiPostJson('/api/pxi/worktree/new', { branch: fd.get('branch'), base: fd.get('base') });
      close();
      const task = String(fd.get('task') || '').trim();
      openClaude({ where: j.name, text: task, send: !!task });
      showToast('✓', 'Worktree ' + j.name + ' created from ' + j.base);
    } catch (e2) {
      err.textContent = e2.message; err.hidden = false;
      ok.disabled = false; ok.textContent = 'Create and start Claude';
    }
  });
  box.querySelector('input').focus();
}

// Ship: the worktree's Claude session, asked to commit, push and open a PR.
// Prefilled, not sent: the user reads it first.
export function shipWorktree(name, base) {
  openClaude({ where: name, latest: true, text:
    'Ship this worktree: look at `git status` and the diff against ' + (base || 'the default branch') +
    ', commit everything with a clear message (split into logical commits if that reads better), push the branch to origin, ' +
    'and open a pull request with `gh pr create` (short title; description says what changed and why). Then show me the PR URL.' });
}

// ------------------------------------------------------------ review → Claude
// "Send to Claude" on a selection or a gutter line: opens the Claude session of
// the worktree the file belongs to (its latest, or a new one) with the lines
// quoted, ready for the comment. "Mention in terminal Claude" adds them to the
// prompt of a terminal claude connected through the IDE bridge.

let clGutterLine = 0, clGutterAt = 0; // the line whose gutter button opened the menu

function clTarget() {
  const info = getSelectedRangeInfo();
  const d = doc_();
  if (info && info.text) return { path: info.path, l1: info.l1, l2: info.l2, text: info.text };
  if (!d || d.isImage) return null;
  const n = (Date.now() - clGutterAt < 30000 && clGutterLine) || d.cur || 1;
  const line = (d.lines && d.lines[n - 1]) || '';
  // Rendered lines are highlighted HTML; DOMParser reads text without running anything.
  const html = typeof line === 'string' ? line : (line?.html || line?.text || '');
  const text = new DOMParser().parseFromString(html, 'text/html').body.textContent || '';
  return { path: d.path, l1: n, l2: n, text };
}

function clReview(t) {
  if (!t) return;
  const m = /^@wt\/([^/]+)\/(.*)$/.exec(t.path);
  const where = m ? m[1] : '';
  const rel = m ? m[2] : t.path;
  const lines = t.l1 === t.l2 ? 'line ' + t.l1 : 'lines ' + t.l1 + '-' + t.l2;
  const ext = rel.split('.').pop() || '';
  const quote = t.text.length > 6000 ? t.text.slice(0, 6000) + '\n…' : t.text;
  openClaude({ where, latest: true, text: 'Review comment on `' + rel + '` ' + lines + ':\n```' + ext + '\n' + quote.replace(/\n$/, '') + '\n```\n' });
}

async function clMention(t) {
  if (!t) return;
  try {
    const j = await apiPostJson('/api/pxi/ide/mention', { path: t.path, l1: t.l1, l2: t.l2 });
    showToast('✓', j.sent ? 'Added to the terminal Claude\'s prompt' : 'No terminal Claude is connected');
  } catch (e) { showToast('!', e.message); }
}

function clMenuItems() {
  if (!SEL_MENU_ITEMS.some(i => i.sel === 'pxi-claude')) SEL_MENU_ITEMS.splice(1, 0, { sel: 'pxi-claude', label: 'Send to Claude', keys: 'Alt+K' });
  const has = SEL_MENU_ITEMS.some(i => i.sel === 'pxi-claude-term');
  if (clIde && !has) SEL_MENU_ITEMS.splice(2, 0, { sel: 'pxi-claude-term', label: 'Mention in terminal Claude' });
  if (!clIde && has) SEL_MENU_ITEMS.splice(SEL_MENU_ITEMS.findIndex(i => i.sel === 'pxi-claude-term'), 1);
}

export function initClaude() {
  clMenuItems();
  // The menu is px0's (selbar.js); these two items are handled here.
  document.addEventListener('click', e => {
    const g = e.target.closest?.('.line-btn[data-l]');
    if (g) { clGutterLine = +g.dataset.l; clGutterAt = Date.now(); return; }
    const b = e.target.closest?.('#sel-menu [data-sel="pxi-claude"], #sel-menu [data-sel="pxi-claude-term"]');
    if (!b) return;
    e.stopPropagation();
    const t = clTarget();
    clGutterLine = 0;
    $('#sel-menu').hidden = true;
    if (b.dataset.sel === 'pxi-claude') clReview(t); else clMention(t);
  }, true);
  document.addEventListener('keydown', e => {
    if (e.altKey && !e.metaKey && !e.ctrlKey && e.code === 'KeyK' && !e.target.closest?.('input, textarea, [contenteditable]')) {
      const t = clTarget();
      if (t && !clActive) { e.preventDefault(); clReview(t); }
    }
  }, true);
  // Sessions or terminal claudes from before this page loaded: watch them.
  // After the page's own load requests: each request in flight at once keeps a gzip writer pooled.
  setTimeout(() => api('/api/pxi/claude/status').then(j => { if (j.runs || j.ide) clStartWatch(); }).catch(() => {}), 2500);
  // A file tab taking over hides the Claude tab (it stays open).
  on('tab:activated', clHide);
  document.addEventListener('click', e => {
    if (clActive && e.target.closest('#tabs .tab')) clHide();
  }, true);
  // Remember the editor selection: clicking into the panel clears it.
  document.addEventListener('selectionchange', () => {
    if (clActive || (!clTab && !clMod && !clIde)) return;
    const info = getSelectedRangeInfo();
    if (info && info.text) clLastSel = { path: info.path, l1: info.l1, l2: info.l2, text: info.text.slice(0, 20000) };
    if (clIde) clSendContext();
  });
  on('tab:activated', () => { clLastSel = null; if (clIde) clSendContext(); });
  document.addEventListener('keydown', e => {
    if (e.key === 'Escape' && e[MOD] && !e.shiftKey && !e.altKey) {
      e.preventDefault();
      toggleClaude();
    }
  }, true);
}
