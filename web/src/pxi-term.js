// web/src/pxi-term.js
// pxi: a terminal panel under the editor (⌃` toggles it, ⌃⇧` opens another
// terminal). Server: fork_term.go.
//
// Memory rule (MAINTAINING.md): nothing loads until the panel is first opened;
// xterm.js is fetched from pxi's own server then and stays loaded (~1 MB of
// script). Closing the panel removes it and every screen and closes the event
// stream; shells keep running on the server, which keeps their recent output,
// and reopening redraws from it.
// Top-level names are prefixed term: the Node fallback bundler shares one scope.
import { $, api, apiPostJson, esc } from './state.js';
import { layout, render } from './renderer.js';
import { showToast } from './ui.js';
import { claudeWatch } from './pxi-claude.js';

const TERM_HEIGHT_KEY = 'pxi.term.height';
const TERM_SCROLLBACK = 3000;

let termPanel = null;
let termBody = null;
let termChips = null;
let termShown = false;
let termEvents = null;
let termActive = 0;
let termLib = null;
let termObserver = null;
const termViews = new Map(); // id -> { term, fit, el, meta, exited }
const termQueues = new Map(); // id -> { buf, timer, chain }

const TERM_ICON = {
  plus: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"><path d="M8 3v10M3 8h10"/></svg>',
  branch: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"><circle cx="4.5" cy="3.5" r="1.5"/><circle cx="4.5" cy="12.5" r="1.5"/><circle cx="11.5" cy="5.5" r="1.5"/><path d="M4.5 5v6M11.5 7c0 2.5-3 3-7 4"/></svg>',
  trash: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><path d="M2.5 4h11M6 4V2.5h4V4M4 4l.7 9.5h6.6L12 4"/></svg>',
  close: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"><path d="M4 4l8 8M12 4l-8 8"/></svg>',
  term: '<svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><rect x="1.5" y="2.5" width="13" height="11" rx="1.5"/><path d="M4.5 6l2 2-2 2M8.5 10.5h3"/></svg>',
};

export const TERM_STATUS_ICON = TERM_ICON.term;

function termLoad() {
  if (termLib) return termLib;
  const add = (tag, attrs) => new Promise((res, rej) => {
    const el = document.createElement(tag);
    Object.assign(el, attrs);
    el.onload = res;
    el.onerror = () => rej(new Error('could not load ' + (attrs.src || attrs.href)));
    document.head.append(el);
  });
  termLib = add('link', { rel: 'stylesheet', href: 'static/pxi/vendor/xterm-5.5.0.css' })
    .then(() => add('script', { src: 'static/pxi/vendor/xterm-5.5.0.js' }))
    .then(() => add('script', { src: 'static/pxi/vendor/addon-fit-0.10.0.js' }))
    .catch(e => { termLib = null; throw e; });
  return termLib;
}

function termEnsureDom() {
  if (termPanel) return;
  termPanel = document.createElement('section');
  termPanel.id = 'pxi-panel';
  termPanel.hidden = true;
  termPanel.innerHTML =
    '<div id="pxi-panel-resizer" title="Drag to resize"></div>' +
    '<header class="pp-head"><span class="pp-title">Terminal</span>' +
    '<div class="pp-chips" id="pp-chips"></div><span class="pp-grow"></span>' +
    '<button class="pp-btn" data-pp="new" title="New terminal (⌃⇧`)">' + TERM_ICON.plus + '</button>' +
    '<button class="pp-btn" data-pp="new-wt" title="New terminal in a worktree">' + TERM_ICON.branch + '</button>' +
    '<button class="pp-btn" data-pp="kill" title="Kill this terminal">' + TERM_ICON.trash + '</button>' +
    '<button class="pp-btn" data-pp="hide" title="Hide the panel (⌃`). Terminals keep running.">' + TERM_ICON.close + '</button></header>' +
    '<div class="pp-menu" id="pp-menu" hidden></div>' +
    '<div class="pp-body" id="pp-body"></div>';
  $('#main').append(termPanel);
  termBody = termPanel.querySelector('#pp-body');
  termChips = termPanel.querySelector('#pp-chips');
  let h = 0;
  try { h = Number(localStorage.getItem(TERM_HEIGHT_KEY)) || 0; } catch {}
  termPanel.style.height = (h || 260) + 'px';
  termPanel.addEventListener('click', termOnClick);
  // Keys typed in a terminal belong to the shell, not to the editor's shortcuts
  // (⌘ shortcuts such as ⌘P still reach the app).
  termBody.addEventListener('keydown', e => { if (!e.metaKey) e.stopPropagation(); });
  termResizer(termPanel.querySelector('#pxi-panel-resizer'));
  termObserver = new ResizeObserver(() => termFitActive());
  termObserver.observe(termBody);
}

function termResizer(handle) {
  let startY = 0, startH = 0;
  const move = e => {
    const max = Math.max(120, $('#main').clientHeight - 120);
    termPanel.style.height = Math.max(90, Math.min(max, startH + (startY - e.clientY))) + 'px';
  };
  const up = () => {
    removeEventListener('mousemove', move);
    removeEventListener('mouseup', up);
    handle.classList.remove('drag');
    try { localStorage.setItem(TERM_HEIGHT_KEY, String(termPanel.offsetHeight)); } catch {}
    layout(); render();
  };
  handle.addEventListener('mousedown', e => {
    e.preventDefault();
    startY = e.clientY; startH = termPanel.offsetHeight;
    handle.classList.add('drag');
    addEventListener('mousemove', move);
    addEventListener('mouseup', up);
  });
}

function termTheme() {
  const cs = getComputedStyle(document.documentElement);
  const v = n => cs.getPropertyValue(n).trim();
  return {
    background: v('--bg'), foreground: v('--fg'), cursor: v('--fg'), cursorAccent: v('--bg'),
    selectionBackground: v('--sel'),
  };
}

function termCreateView(meta) {
  if (termViews.has(meta.id)) return termViews.get(meta.id);
  const el = document.createElement('div');
  el.className = 'pp-term';
  el.hidden = true;
  termBody.append(el);
  const cs = getComputedStyle(document.body);
  // xterm.js 5.5 leaves a window "resize" listener behind on dispose; note
  // what it adds to window while being set up, and remove it ourselves.
  const winListeners = [];
  const origAdd = window.addEventListener;
  window.addEventListener = function (type, fn, opts) {
    winListeners.push([type, fn, opts]);
    return origAdd.call(this, type, fn, opts);
  };
  let term, fit;
  try {
    term = new window.Terminal({
      fontFamily: cs.getPropertyValue('--mono').trim() || 'Menlo, monospace',
      fontSize: 12.5, lineHeight: 1.15, scrollback: TERM_SCROLLBACK, cursorBlink: true,
      allowProposedApi: false, theme: termTheme(), macOptionIsMeta: true,
    });
    fit = new window.FitAddon.FitAddon();
    term.loadAddon(fit);
    term.open(el);
  } finally {
    window.addEventListener = origAdd;
  }
  term.onData(d => termSend(meta.id, d));
  const view = { term, fit, el, meta, exited: !meta.alive, winListeners };
  termViews.set(meta.id, view);
  return view;
}

function termDispose(v) {
  v.term.dispose();
  for (const [type, fn, opts] of v.winListeners) window.removeEventListener(type, fn, opts);
  v.el.remove();
}

function termSend(id, data) {
  let q = termQueues.get(id);
  if (!q) termQueues.set(id, q = { buf: '', timer: 0, chain: Promise.resolve() });
  q.buf += data;
  if (q.timer) return;
  // Keystrokes arriving together go in one request, in order.
  q.timer = setTimeout(() => {
    const chunk = q.buf;
    q.buf = '';
    q.timer = 0;
    q.chain = q.chain.then(() => apiPostJson('/api/pxi/term/input', { id, data: chunk })).catch(() => {});
  }, 4);
}

function termFitActive() {
  const v = termViews.get(termActive);
  if (!v || !termShown || !termPanel || termPanel.hidden) return;
  try { v.fit.fit(); } catch { return; }
  const { cols, rows } = v.term;
  if (v.meta.cols === cols && v.meta.rows === rows) return;
  v.meta.cols = cols; v.meta.rows = rows;
  if (!v.exited) apiPostJson('/api/pxi/term/resize', { id: v.meta.id, cols, rows }).catch(() => {});
}

function termDrawChips() {
  termChips.innerHTML = [...termViews.values()].map(v =>
    '<button class="pp-chip' + (v.meta.id === termActive ? ' active' : '') + (v.exited ? ' exited' : '') + '" data-term="' + v.meta.id + '" title="' + esc(v.meta.cwd || '') + '">' +
    TERM_ICON.term + '<span>' + esc(v.meta.label) + '</span></button>').join('');
}

function termSelect(id) {
  termActive = id;
  for (const v of termViews.values()) v.el.hidden = v.meta.id !== id;
  termDrawChips();
  const v = termViews.get(id);
  if (v) {
    requestAnimationFrame(() => { termFitActive(); v.term.focus(); });
  }
}

function termConnect() {
  if (termEvents) return;
  termEvents = new EventSource('api/pxi/term/events');
  termEvents.onmessage = e => {
    let ev;
    try { ev = JSON.parse(e.data); } catch { return; }
    const v = termViews.get(ev.t);
    if (!v) { termSync(); return; } // opened from elsewhere
    if (ev.d) {
      const bin = atob(ev.d);
      const bytes = new Uint8Array(bin.length);
      for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
      v.term.write(bytes);
    }
    if (ev.exit !== undefined && !v.exited) {
      v.exited = true;
      v.term.write('\r\n\x1b[2m[process exited with code ' + ev.exit + ']\x1b[0m\r\n');
      termDrawChips();
    }
  };
  // EventSource reconnects by itself; the server replays each terminal's kept
  // output, so screens are cleared first to avoid printing it twice.
  termEvents.onopen = () => { for (const v of termViews.values()) v.term.reset(); };
}

async function termSync() {
  const j = await api('/api/pxi/term/list');
  const ids = new Set();
  for (const meta of j.terminals || []) {
    ids.add(meta.id);
    termCreateView(meta);
  }
  for (const [id, v] of termViews) if (!ids.has(id)) { termDispose(v); termViews.delete(id); }
  if (!termViews.has(termActive)) termActive = termViews.keys().next().value || 0;
  termDrawChips();
  return termViews.size;
}

async function termOpenNew(cwd = '') {
  try {
    const v0 = termViews.get(termActive);
    const meta = await apiPostJson('/api/pxi/term/open', { cwd, cols: v0?.term.cols || 100, rows: v0?.term.rows || 20 });
    termCreateView(meta);
    termSelect(meta.id);
  } catch (e) {
    showToast('!', e.message || String(e), 5000);
  }
}

/** Opens the panel; with cwd ("@wt/<name>") it also opens a terminal there. */
export async function showTerminal({ cwd = null, fresh = false } = {}) {
  termEnsureDom();
  try {
    await termLoad();
  } catch (e) {
    showToast('!', e.message, 5000);
    return;
  }
  termShown = true;
  termPanel.hidden = false;
  claudeWatch(); // `claude` in these shells can connect to pxi (fork_ide.go)
  document.body.classList.add('pxi-panel-open');
  layout(); render();
  const count = await termSync();
  termConnect();
  if (cwd !== null || fresh || !count) await termOpenNew(cwd || '');
  else termSelect(termActive || termViews.keys().next().value);
}

/** Hides the panel and frees its screens and the panel itself. Shells keep
 *  running on the server. */
export function hideTerminal() {
  claudeWatch(false);
  if (!termPanel || !termShown) return;
  termShown = false;
  termEvents?.close();
  termEvents = null;
  for (const v of termViews.values()) termDispose(v);
  termViews.clear();
  for (const q of termQueues.values()) clearTimeout(q.timer);
  termQueues.clear();
  termObserver?.disconnect();
  termPanel.remove();
  termPanel = termBody = termChips = null;
  // xterm.js itself stays loaded once used (about 1 MB): unloading it does
  // not give the memory back, and loading it again would add a second copy.
  document.body.classList.remove('pxi-panel-open');
  layout(); render();
}

export function toggleTerminal() {
  if (termShown) hideTerminal(); else showTerminal();
}

async function termKill(id) {
  if (!id) return;
  await apiPostJson('/api/pxi/term/close', { id }).catch(() => {});
  const v = termViews.get(id);
  if (v) { termDispose(v); termViews.delete(id); }
  if (!termViews.size) { hideTerminal(); return; }
  termSelect(termViews.keys().next().value);
}

async function termWorktreeMenu() {
  const menu = termPanel.querySelector('#pp-menu');
  if (!menu.hidden) { menu.hidden = true; return; }
  let list = [];
  try { list = (await api('/api/worktrees')).worktrees || []; } catch {}
  list = list.filter(w => !w.prunable);
  menu.innerHTML = list.length
    ? list.map(w => '<button class="pp-menu-item" data-wt="' + esc(w.name) + '">' + TERM_ICON.branch + '<span>' + esc(w.name) + '</span>' +
      (w.branch ? '<span class="pp-dim">' + esc(w.branch) + '</span>' : '') + '</button>').join('')
    : '<div class="pp-menu-empty">No other worktrees</div>';
  menu.hidden = false;
}

function termOnClick(e) {
  const menuItem = e.target.closest('[data-wt]');
  if (menuItem) {
    termPanel.querySelector('#pp-menu').hidden = true;
    termOpenNew('@wt/' + menuItem.dataset.wt);
    return;
  }
  const chip = e.target.closest('[data-term]');
  if (chip) { termSelect(Number(chip.dataset.term)); return; }
  const b = e.target.closest('[data-pp]');
  if (!b) return;
  const act = b.dataset.pp;
  if (act !== 'new-wt') termPanel.querySelector('#pp-menu').hidden = true;
  if (act === 'new') termOpenNew('');
  if (act === 'new-wt') termWorktreeMenu();
  if (act === 'kill') termKill(termActive);
  if (act === 'hide') hideTerminal();
}

/** ⌃` toggles the panel and ⌃⇧` opens another terminal, as in VS Code. */
export function initTerminal() {
  addEventListener('keydown', e => {
    if (!e.ctrlKey || e.metaKey || e.altKey || e.code !== 'Backquote') return;
    e.preventDefault();
    e.stopPropagation();
    if (e.shiftKey) showTerminal({ fresh: true }); else toggleTerminal();
  }, true);
  // Re-theme open screens when the theme changes.
  new MutationObserver(() => { for (const v of termViews.values()) v.term.options.theme = termTheme(); })
    .observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
}
