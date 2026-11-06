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
import { getSelectedRangeInfo } from './selbar.js';

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
  watch: clStartWatch,
  close: clClose,
};

function clStartWatch() {
  if (clWatch) return;
  clWatch = new EventSource('api/pxi/claude/watch');
  clWatch.onmessage = e => {
    try { clRuns = JSON.parse(e.data).runs || []; } catch { clRuns = []; }
    const asking = clRuns.reduce((n, r) => n + (r.asking || 0), 0);
    clSetBadge(asking);
    clMod?.runs?.(clRuns);
    // Nothing running and no tab: stop listening.
    if (!clRuns.length && !clTab) { clWatch.close(); clWatch = null; }
  };
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

function clShow() {
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
    clMod.focus?.();
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
  if (clWatch && !clRuns.length) { clWatch.close(); clWatch = null; }
}

// Opens the Claude tab; opts: { where, session, text } (where: worktree name).
export function openClaude(opts) {
  if (opts) clPending = opts;
  clShow();
  if (clMod && clPending) { clMod.open(clPending); clPending = null; }
}

export function toggleClaude() {
  if (clActive) clHide(); else clShow();
}

export function initClaude() {
  // A file tab taking over hides the Claude tab (it stays open).
  on('tab:activated', clHide);
  document.addEventListener('click', e => {
    if (clActive && e.target.closest('#tabs .tab')) clHide();
  }, true);
  // Remember the editor selection: clicking into the panel clears it.
  document.addEventListener('selectionchange', () => {
    if (clActive || (!clTab && !clMod)) return;
    const info = getSelectedRangeInfo();
    if (info && info.text) clLastSel = { path: info.path, l1: info.l1, l2: info.l2, text: info.text.slice(0, 20000) };
  });
  on('tab:activated', () => { clLastSel = null; });
  document.addEventListener('keydown', e => {
    if (e.key === 'Escape' && e[MOD] && !e.shiftKey && !e.altKey) {
      e.preventDefault();
      toggleClaude();
    }
  }, true);
}
