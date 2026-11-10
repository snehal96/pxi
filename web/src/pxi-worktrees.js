// web/src/pxi-worktrees.js
// pxi: the Worktrees view. Every git worktree of the open repository in one
// list, each with the files it changed since it branched off the base branch
// (commits + uncommitted + untracked), and a warning on files that more than
// one worktree changes. Clicking a file opens its diff with the worktree's
// other files queued in the review strip ([f / ]f, ✓ Viewed).
// Styles: web/pxi/shell.css. Server side: worktrees.go (/api/worktrees, @wt/<name>/<path> files).
import { $, esc, api, apiPostJson } from './state.js';
import { showToast } from './ui.js';
import { openFile } from './tabs.js';
import { showTerminal } from './pxi-term.js';
import { openClaude, newWorktreeDialog, shipWorktree } from './pxi-claude.js';
import { openWorktreeWalkthrough } from './pxi-walkthrough.js';

const POLL_MS = 3000;
const ROW_CAP = 300;
const COLLAPSED_KEY = 'pxi.wt.collapsed';
const OVERLAPS_KEY = '/overlaps'; // in the collapsed set; no worktree name has a '/'

const STATUS = {
  M: ['git-M', 'Modified'], A: ['git-A', 'Added'], D: ['git-D', 'Deleted'], U: ['git-untracked', 'Untracked'],
};

let wtRoot = null;        // #wt-view
let wtBody = null;        // its scrolling body
let wtData = null;        // last /api/worktrees
let wtLastSig = '';
let wtTimer = 0;
let wtVisible = false;
let wtOnCount = () => {};
const wtCollapsed = new Set(readCollapsed());
let wtHidden = new Set();   // worktrees the user hid in this repository (per root, in this browser)
let wtShowHidden = false;
let wtMenu = null;          // the right-click menu while open

const wtHiddenKey = () => 'pxi.wt.hidden:' + (wtData?.root || '');
function wtLoadHidden() {
  try { wtHidden = new Set(JSON.parse(localStorage.getItem(wtHiddenKey()) || '[]')); } catch { wtHidden = new Set(); }
}
function wtSaveHidden() {
  try { localStorage.setItem(wtHiddenKey(), JSON.stringify([...wtHidden])); } catch {}
}

function readCollapsed() {
  try { return JSON.parse(localStorage.getItem(COLLAPSED_KEY) || '[]'); } catch { return []; }
}
function saveCollapsed() {
  try { localStorage.setItem(COLLAPSED_KEY, JSON.stringify([...wtCollapsed])); } catch {}
}

const ICON = {
  branch: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"><circle cx="4.5" cy="3.5" r="1.5"/><circle cx="4.5" cy="12.5" r="1.5"/><circle cx="11.5" cy="5.5" r="1.5"/><path d="M4.5 5v6M11.5 7c0 2.5-3 3-7 4"/></svg>',
  chevron: '<svg class="wt-chev" viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M6 4l4 4-4 4"/></svg>',
  warn: '<svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round"><path d="M8 2l6.5 11.5h-13z"/><path d="M8 6.5v3.2M8 11.6v.1" stroke-linecap="round"/></svg>',
  refresh: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9"/><path d="M13.5 2.5v3h-3"/></svg>',
  review: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><path d="M1.5 8s2.5-4.5 6.5-4.5S14.5 8 14.5 8 12 12.5 8 12.5 1.5 8 1.5 8z"/><circle cx="8" cy="8" r="2"/></svg>',
  plus: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"><path d="M8 3v10M3 8h10"/></svg>',
  ship: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><path d="M8 13V3M4 7l4-4 4 4"/><path d="M3 13.5h10"/></svg>',
  claude: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"><path d="M8 2v12M2 8h12M3.8 3.8l8.4 8.4M12.2 3.8l-8.4 8.4"/></svg>',
  term: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><rect x="1.5" y="2.5" width="13" height="11" rx="1.5"/><path d="M4.5 6l2 2-2 2M8.5 10.5h3"/></svg>',
  walk: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><path d="M9 1.5L3.5 9H8l-1 5.5L12.5 7H8z"/></svg>',
  folder: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round"><path d="M1.5 4.5v8h13v-7h-6.5l-1.5-1.5h-5z"/></svg>',
};

export function worktreeCount() {
  return wtData ? wtData.worktrees.filter(w => !w.prunable && !w.main && !wtHidden.has(w.name)).length : 0;
}

/** Builds the view inside the sidebar. onCountChange(count, overlaps) feeds the activity bar badge. */
let wtSide = null;

/** Starts the badge; the view's DOM is built the first time it is opened. */
export function initWorktrees(side, onCountChange) {
  wtOnCount = onCountChange || wtOnCount;
  wtSide = side;
  document.addEventListener('visibilitychange', schedule);
  refresh(false); // fill the activity bar badge
}

function wtEnsureDom() {
  if (wtRoot) return;
  wtRoot = document.createElement('section');
  wtRoot.id = 'wt-view';
  wtRoot.className = 'wt-view';
  wtRoot.innerHTML =
    '<header class="panel-head wt-head-bar"><span class="wt-title">Worktrees</span>' +
    '<span class="wt-base" id="wt-base"></span>' +
    '<button class="mini" id="wt-new" title="New worktree with a Claude session">' + ICON.plus + '</button>' +
    '<button class="mini" id="wt-refresh" title="Refresh">' + ICON.refresh + '</button></header>' +
    '<div class="wt-body" id="wt-body"></div>';
  wtSide.insertBefore(wtRoot, wtSide.querySelector('.side-foot'));
  wtBody = wtRoot.querySelector('#wt-body');
  wtRoot.querySelector('#wt-refresh').addEventListener('click', () => refresh(true));
  wtRoot.querySelector('#wt-new').addEventListener('click', newWorktreeDialog);
  wtBody.addEventListener('click', onClick);
  wtBody.addEventListener('contextmenu', wtContextMenu);
}

export function setWorktreesVisible(on) {
  if (on === wtVisible) return;
  wtVisible = on;
  if (on) {
    wtEnsureDom();
    wtLastSig = ''; // draw on the next refresh
    refresh(false);
  } else if (wtBody) {
    wtCloseMenu();
    wtBody.replaceChildren(); // a closed view keeps no rows in memory
  }
  schedule();
}

function schedule() {
  clearTimeout(wtTimer);
  if (document.hidden) return;
  // Poll while the view is open; once a minute otherwise, just for the badge.
  wtTimer = setTimeout(() => refresh(false), wtVisible ? POLL_MS : 60000);
}

async function refresh(force) {
  try {
    const j = await api('/api/worktrees', force ? { refresh: 1 } : undefined);
    const rootChanged = wtData?.root !== j.root;
    wtData = { root: j.root, worktrees: j.worktrees || [] };
    if (rootChanged) wtLoadHidden();
    const sig = JSON.stringify(wtData.worktrees);
    if (sig !== wtLastSig || force) {
      wtLastSig = sig;
      renderWorktrees();
    }
  } catch (e) {
    if (wtBody && wtVisible) wtBody.innerHTML = '<div class="wt-empty">Could not list worktrees: ' + esc(e.message || String(e)) + '</div>';
  } finally {
    schedule();
  }
}

// A file's other worktrees, leaving out hidden ones.
const wtOverlapOf = f => (f.overlap || []).filter(n => !wtHidden.has(n));

function overlapsOf(list) {
  const map = new Map();
  for (const w of list) for (const f of w.files) {
    if (wtOverlapOf(f).length) {
      if (!map.has(f.path)) map.set(f.path, new Set());
      map.get(f.path).add(w.name);
    }
  }
  return map;
}

function splitPath(p) {
  const i = p.lastIndexOf('/');
  return i < 0 ? ['', p] : [p.slice(0, i), p.slice(i + 1)];
}

function renderWorktrees() {
  if (!wtData) return;
  const shown = wtData.worktrees.filter(w => !w.prunable && !wtHidden.has(w.name));
  const hidden = wtData.worktrees.filter(w => wtHidden.has(w.name));
  const overlaps = overlapsOf(shown);
  wtOnCount(shown.filter(w => !w.main).length, overlaps.size);
  const list = wtShowHidden ? shown.concat(hidden) : shown;
  if (!wtBody || !wtVisible) return; // closed: only the badge
  const base = list.find(w => w.baseRef)?.baseRef;
  const baseEl = $('#wt-base');
  if (baseEl) baseEl.textContent = base ? 'vs ' + base : '';

  if (!list.some(w => !w.main) && !hidden.length) {
    wtBody.innerHTML = (list[0] ? wtCard(list[0], overlaps) : '') +
      '<div class="wt-empty"><p>No other worktrees in this repository.</p>' +
      '<p class="wt-faint">Worktrees made by Claude Code, another agent, or <code>git worktree add</code> show up here with the changes each one makes.</p></div>';
    return;
  }

  let html = '';
  if (overlaps.size) {
    const ovOpen = !wtCollapsed.has(OVERLAPS_KEY);
    html += '<div class="wt-overlaps' + (ovOpen ? ' open' : '') + '"><div class="wt-overlaps-head" data-toggle="' + OVERLAPS_KEY + '" title="' + (ovOpen ? 'Hide' : 'Show') + ' the files">' +
      ICON.chevron + ICON.warn +
      '<span>' + overlaps.size + ' file' + (overlaps.size > 1 ? 's' : '') + ' changed in more than one worktree</span></div>';
    if (ovOpen) for (const [path, names] of overlaps) {
      const [dir, name] = splitPath(path);
      html += '<div class="wt-ov-row" title="' + esc(path) + '"><span class="wt-fname">' + esc(name) + '</span>' +
        (dir ? '<span class="wt-fdir">' + esc(dir) + '</span>' : '') +
        '<span class="wt-ov-names">' + [...names].map(n => '<span class="wt-chip">' + esc(n) + '</span>').join('') + '</span></div>';
    }
    html += '</div>';
  }

  for (const w of list) html += wtCard(w);
  if (hidden.length) {
    html += '<div class="wt-faint wt-hidden-foot"><button class="wt-link" data-show-hidden>' +
      (wtShowHidden ? 'Hide' : 'Show') + ' ' + hidden.length + ' hidden worktree' + (hidden.length > 1 ? 's' : '') + '</button></div>';
  }
  wtBody.innerHTML = html;
}

function wtCard(w) {
  let html = '';
  {
    const open = !wtCollapsed.has(w.name);
    const ov = w.files.filter(f => wtOverlapOf(f).length).length;
    const meta = [];
    if (w.missing) meta.push('<span class="wt-warn" title="' + w.missing + ' of its ' + w.tracked + ' tracked files are gone from its folder">folder emptied</span>');
    if (w.ahead) meta.push('<span title="Commits since it branched off ' + esc(w.baseRef || 'the base') + '">↑' + w.ahead + '</span>');
    if (!w.missing) meta.push('<span title="Files changed since it branched off">' + w.files.length + ' file' + (w.files.length === 1 ? '' : 's') + '</span>');
    if (w.dirty && !w.missing) meta.push('<span class="wt-dirty" title="Uncommitted files">●' + w.dirty + '</span>');
    if (ov) meta.push('<span class="wt-warn" title="Files another worktree also changes">' + ICON.warn + ov + '</span>');

    html += '<div class="wt-card' + (open ? ' open' : '') + (wtHidden.has(w.name) ? ' wt-is-hidden' : '') + '" data-wt="' + esc(w.name) + '">' +
      '<div class="wt-head" data-toggle="' + esc(w.name) + '" title="' + esc(w.path) + '">' + ICON.chevron +
      (w.main ? '<span class="wt-icon" title="The folder pxi has open">' + ICON.folder + '</span>' : '<span class="wt-icon">' + ICON.branch + '</span>') +
      '<span class="wt-name">' + esc(w.name) + '</span>' +
      (w.branch && w.branch !== w.name ? '<span class="wt-branch">' + esc(w.branch) + '</span>' : (!w.branch ? '<span class="wt-branch">detached</span>' : '')) +
      '<span class="grow"></span><span class="wt-meta">' + meta.join('') + '</span>' +
      '<button class="wt-act" data-wt-claude="' + esc(w.name) + '" title="Claude in ' + esc(w.name) + '">' + ICON.claude + '</button>' +
      '<button class="wt-act" data-wt-term="' + esc(w.name) + '" title="Open a terminal in ' + esc(w.name) + '">' + ICON.term + '</button>' +
      ((w.files.length || w.ahead) ? '<button class="wt-act" data-ship="' + esc(w.name) + '" title="Ship: ask Claude to commit, push and open a PR">' + ICON.ship + '</button>' : '') +
      (w.files.length ? '<button class="wt-act" data-review="' + esc(w.name) + '" title="Review every change in ' + esc(w.name) + '">' + ICON.review + '</button>' : '') +
      (w.files.length ? '<button class="wt-act" data-walk="' + esc(w.name) + '" title="Walkthrough: early exits, error paths and removed checks in ' + esc(w.name) + '">' + ICON.walk + '</button>' : '') +
      '</div>';
    if (open) {
      html += '<div class="wt-files">';
      if (w.error) html += '<div class="wt-faint wt-err">' + esc(w.error) + '</div>';
      if (w.missing) {
        html += '<div class="wt-faint wt-none wt-emptied">Its folder is missing ' + w.missing + ' of ' + w.tracked +
          ' tracked files, so pxi does not list them as changes or count them as overlaps. Often a scratch copy an agent cleaned up. ' +
          'If you no longer need it: <code>git worktree remove --force ' + esc(w.path) + '</code></div>';
      } else if (!w.files.length) html += '<div class="wt-faint wt-none">No changes yet</div>';
      for (const f of w.files.slice(0, ROW_CAP)) {
        const [dir, name] = splitPath(f.path);
        const [cls, label] = STATUS[f.status] || STATUS.M;
        html += '<div class="wt-file" data-wt="' + esc(w.name) + '" data-path="' + esc(f.path) + '" title="' + esc(f.path) + ' — ' + label + '">' +
          '<span class="wt-fname">' + esc(name) + '</span>' +
          (dir ? '<span class="wt-fdir">' + esc(dir) + '</span>' : '') +
          '<span class="grow"></span>' +
          (wtOverlapOf(f).length ? '<span class="wt-warn" title="Also changed in ' + esc(wtOverlapOf(f).join(', ')) + '">' + ICON.warn + '</span>' : '') +
          '<span class="wt-st ' + cls + '">' + esc(f.status) + '</span></div>';
      }
      if (w.files.length > ROW_CAP) html += '<div class="wt-faint wt-none">' + (w.files.length - ROW_CAP) + ' more files: use Review to step through all of them</div>';
      html += '</div>';
    }
    html += '</div>';
  }
  return html;
}

/* ---------- right-click: hide, unhide, delete ---------- */

function wtCloseMenu() {
  if (!wtMenu) return;
  wtMenu.remove();
  wtMenu = null;
  document.removeEventListener('pointerdown', wtMenuOutside, true);
  document.removeEventListener('keydown', wtMenuKey, true);
}
function wtMenuOutside(e) { if (wtMenu && !wtMenu.contains(e.target)) wtCloseMenu(); }
function wtMenuKey(e) { if (e.key === 'Escape') { e.preventDefault(); wtCloseMenu(); } }

function wtContextMenu(e) {
  const head = e.target.closest('.wt-head');
  const w = head && wtData?.worktrees.find(x => x.name === head.dataset.toggle);
  if (!w) return;
  e.preventDefault();
  wtCloseMenu();
  const items = [];
  if (wtHidden.has(w.name)) items.push(['unhide', 'Show again']);
  else if (!w.main) items.push(['hide', 'Hide (stop tracking here)']);
  items.push(['copy', 'Copy Path']);
  if (!w.main) items.push(['delete', 'Delete Worktree…']);
  wtMenu = document.createElement('div');
  wtMenu.id = 'wt-menu';
  wtMenu.setAttribute('role', 'menu');
  for (const [act, label] of items) {
    const b = document.createElement('button');
    b.className = 'sel-menu-item' + (act === 'delete' ? ' wt-danger' : '');
    b.setAttribute('role', 'menuitem');
    b.textContent = label;
    b.addEventListener('click', () => { wtCloseMenu(); wtMenuAct(act, w); });
    wtMenu.append(b);
  }
  document.body.append(wtMenu);
  const mw = wtMenu.offsetWidth, mh = wtMenu.offsetHeight;
  wtMenu.style.left = Math.max(4, e.clientX + mw > innerWidth - 4 ? e.clientX - mw : e.clientX) + 'px';
  wtMenu.style.top = Math.max(4, e.clientY + mh > innerHeight - 4 ? e.clientY - mh : e.clientY) + 'px';
  document.addEventListener('pointerdown', wtMenuOutside, true);
  document.addEventListener('keydown', wtMenuKey, true);
}

async function wtMenuAct(act, w) {
  if (act === 'hide' || act === 'unhide') {
    if (act === 'hide') wtHidden.add(w.name); else wtHidden.delete(w.name);
    wtSaveHidden();
    renderWorktrees();
    if (act === 'hide') showToast('', 'Hid ' + w.name + '. "Show hidden worktrees" at the bottom brings it back.');
    return;
  }
  if (act === 'copy') {
    try { await navigator.clipboard.writeText(w.path); showToast('', 'Copied ' + w.path); } catch { showToast('', w.path); }
    return;
  }
  if (act !== 'delete') return;
  const what = w.branch ? 'Its branch ' + w.branch + ' is kept.' : 'It has no branch (detached): commits only it has may be lost.';
  if (!confirm('Delete worktree ' + w.name + '?\n\nThis deletes the folder ' + w.path + '. ' + what)) return;
  let r = await wtRemove(w.name, false);
  if (r.dirty) {
    const n = w.missing ? w.missing + ' files are already gone from it' : (w.dirty || 'some') + ' uncommitted or untracked files';
    if (!confirm(w.name + ' has changes that are not committed (' + n + ').\n\nDelete it anyway? Those changes are lost.')) return;
    r = await wtRemove(w.name, true);
  }
  if (r.error) { showToast('', 'Could not delete ' + w.name + ': ' + r.error); return; }
  wtHidden.delete(w.name);
  wtSaveHidden();
  showToast('', 'Deleted worktree ' + w.name + (r.branch ? ' (branch ' + r.branch + ' kept)' : ''));
  refresh(true);
}

async function wtRemove(name, force) {
  try {
    return await apiPostJson('/api/pxi/worktree/remove', { name, force }, { timeout: 120000 });
  } catch (e) {
    return e.body || { error: e.message || String(e) };
  }
}

function filesFor(w) {
  return w.files.map(f => ({ path: '@wt/' + w.name + '/' + f.path, status: f.status === 'U' ? 'A' : f.status }));
}

function openWorktreeFile(w, path) {
  for (const el of wtBody.querySelectorAll('.wt-file.active')) el.classList.remove('active');
  wtBody.querySelector('.wt-file[data-wt="' + CSS.escape(w.name) + '"][data-path="' + CSS.escape(path) + '"]')?.classList.add('active');
  openFile('@wt/' + w.name + '/' + path, {
    view: 'diff',
    turnFiles: filesFor(w),
    turnLabel: w.name + (w.baseRef ? ' vs ' + w.baseRef : ''),
  });
}

function onClick(e) {
  if (!wtData) return;
  if (e.target.closest('[data-show-hidden]')) {
    wtShowHidden = !wtShowHidden;
    renderWorktrees();
    return;
  }
  const find = name => wtData.worktrees.find(w => w.name === name);
  const shipBtn = e.target.closest('[data-ship]');
  if (shipBtn) {
    e.stopPropagation();
    const w = find(shipBtn.dataset.ship);
    shipWorktree(shipBtn.dataset.ship, w?.baseRef);
    return;
  }
  const clBtn = e.target.closest('[data-wt-claude]');
  if (clBtn) {
    e.stopPropagation();
    openClaude({ where: clBtn.dataset.wtClaude });
    return;
  }
  const termBtn = e.target.closest('[data-wt-term]');
  if (termBtn) {
    e.stopPropagation();
    showTerminal({ cwd: '@wt/' + termBtn.dataset.wtTerm });
    return;
  }
  const walkBtn = e.target.closest('[data-walk]');
  if (walkBtn) {
    e.stopPropagation();
    openWorktreeWalkthrough(walkBtn.dataset.walk);
    return;
  }
  const rev = e.target.closest('[data-review]');
  if (rev) {
    e.stopPropagation();
    const w = find(rev.dataset.review);
    if (w?.files.length) openWorktreeFile(w, w.files[0].path);
    return;
  }
  const file = e.target.closest('.wt-file');
  if (file) {
    const w = find(file.dataset.wt);
    if (w) openWorktreeFile(w, file.dataset.path);
    return;
  }
  const head = e.target.closest('[data-toggle]');
  if (head) {
    const n = head.dataset.toggle;
    if (wtCollapsed.has(n)) wtCollapsed.delete(n); else wtCollapsed.add(n);
    saveCollapsed();
    renderWorktrees();
  }
}
