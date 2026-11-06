// web/src/pxi-worktrees.js
// pxi: the Worktrees view. Every git worktree of the open repository in one
// list, each with the files it changed since it branched off the base branch
// (commits + uncommitted + untracked), and a warning on files that more than
// one worktree changes. Clicking a file opens its diff with the worktree's
// other files queued in the review strip ([f / ]f, ✓ Viewed).
// Styles: web/pxi/shell.css. Server side: worktrees.go (/api/worktrees, @wt/<name>/<path> files).
import { $, esc, api } from './state.js';
import { openFile } from './tabs.js';

const POLL_MS = 3000;
const ROW_CAP = 300;
const COLLAPSED_KEY = 'pxi.wt.collapsed';

const STATUS = {
  M: ['git-M', 'Modified'], A: ['git-A', 'Added'], D: ['git-D', 'Deleted'], U: ['git-untracked', 'Untracked'],
};

let root = null;        // #wt-view
let body = null;        // its scrolling body
let data = null;        // last /api/worktrees
let lastSig = '';
let timer = 0;
let visible = false;
let onCount = () => {};
const collapsed = new Set(readCollapsed());

function readCollapsed() {
  try { return JSON.parse(localStorage.getItem(COLLAPSED_KEY) || '[]'); } catch { return []; }
}
function saveCollapsed() {
  try { localStorage.setItem(COLLAPSED_KEY, JSON.stringify([...collapsed])); } catch {}
}

const ICON = {
  branch: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"><circle cx="4.5" cy="3.5" r="1.5"/><circle cx="4.5" cy="12.5" r="1.5"/><circle cx="11.5" cy="5.5" r="1.5"/><path d="M4.5 5v6M11.5 7c0 2.5-3 3-7 4"/></svg>',
  chevron: '<svg class="wt-chev" viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M6 4l4 4-4 4"/></svg>',
  warn: '<svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round"><path d="M8 2l6.5 11.5h-13z"/><path d="M8 6.5v3.2M8 11.6v.1" stroke-linecap="round"/></svg>',
  refresh: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9"/><path d="M13.5 2.5v3h-3"/></svg>',
  review: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><path d="M1.5 8s2.5-4.5 6.5-4.5S14.5 8 14.5 8 12 12.5 8 12.5 1.5 8 1.5 8z"/><circle cx="8" cy="8" r="2"/></svg>',
  folder: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round"><path d="M1.5 4.5v8h13v-7h-6.5l-1.5-1.5h-5z"/></svg>',
};

export function worktreeCount() {
  return data ? data.worktrees.filter(w => !w.prunable).length : 0;
}

/** Builds the view inside the sidebar. onCountChange(count, overlaps) feeds the activity bar badge. */
export function initWorktrees(side, onCountChange) {
  onCount = onCountChange || onCount;
  root = document.createElement('section');
  root.id = 'wt-view';
  root.className = 'wt-view';
  root.innerHTML =
    '<header class="panel-head wt-head-bar"><span class="wt-title">Worktrees</span>' +
    '<span class="wt-base" id="wt-base"></span>' +
    '<button class="mini" id="wt-refresh" title="Refresh">' + ICON.refresh + '</button></header>' +
    '<div class="wt-body" id="wt-body"></div>';
  side.insertBefore(root, side.querySelector('.side-foot'));
  body = root.querySelector('#wt-body');

  root.querySelector('#wt-refresh').addEventListener('click', () => refresh(true));
  body.addEventListener('click', onClick);
  document.addEventListener('visibilitychange', schedule);
  refresh(false); // fill the badge even before the view is opened
}

export function setWorktreesVisible(on) {
  visible = on;
  if (on) refresh(false);
  schedule();
}

function schedule() {
  clearTimeout(timer);
  if (document.hidden) return;
  // Poll fast while the view is open; slowly otherwise, just for the badge.
  timer = setTimeout(() => refresh(false), visible ? POLL_MS : POLL_MS * 5);
}

async function refresh(force) {
  try {
    const j = await api('/api/worktrees', force ? { refresh: 1 } : undefined);
    data = { root: j.root, worktrees: j.worktrees || [] };
    const sig = JSON.stringify(data.worktrees);
    if (sig !== lastSig || force) {
      lastSig = sig;
      render();
    }
  } catch (e) {
    if (body && visible) body.innerHTML = '<div class="wt-empty">Could not list worktrees: ' + esc(e.message || String(e)) + '</div>';
  } finally {
    schedule();
  }
}

function overlapsOf(list) {
  const map = new Map();
  for (const w of list) for (const f of w.files) {
    if (f.overlap?.length) {
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

function render() {
  if (!body || !data) return;
  const list = data.worktrees.filter(w => !w.prunable);
  const overlaps = overlapsOf(list);
  onCount(list.length, overlaps.size);
  const base = list.find(w => w.baseRef)?.baseRef;
  const baseEl = $('#wt-base');
  if (baseEl) baseEl.textContent = base ? 'vs ' + base : '';

  if (!list.length) {
    body.innerHTML =
      '<div class="wt-empty"><p>No other worktrees in this repository.</p>' +
      '<p class="wt-faint">Worktrees made by Claude Code, another agent, or <code>git worktree add</code> show up here with the changes each one makes.</p></div>';
    return;
  }

  let html = '';
  if (overlaps.size) {
    html += '<div class="wt-overlaps"><div class="wt-overlaps-head">' + ICON.warn +
      '<span>' + overlaps.size + ' file' + (overlaps.size > 1 ? 's' : '') + ' changed in more than one worktree</span></div>';
    for (const [path, names] of overlaps) {
      const [dir, name] = splitPath(path);
      html += '<div class="wt-ov-row" title="' + esc(path) + '"><span class="wt-fname">' + esc(name) + '</span>' +
        (dir ? '<span class="wt-fdir">' + esc(dir) + '</span>' : '') +
        '<span class="wt-ov-names">' + [...names].map(n => '<span class="wt-chip">' + esc(n) + '</span>').join('') + '</span></div>';
    }
    html += '</div>';
  }

  for (const w of list) {
    const open = !collapsed.has(w.name);
    const ov = w.files.filter(f => f.overlap?.length).length;
    const meta = [];
    if (w.ahead) meta.push('<span title="Commits since it branched off ' + esc(w.baseRef || 'the base') + '">↑' + w.ahead + '</span>');
    meta.push('<span title="Files changed since it branched off">' + w.files.length + ' file' + (w.files.length === 1 ? '' : 's') + '</span>');
    if (w.dirty) meta.push('<span class="wt-dirty" title="Uncommitted files">●' + w.dirty + '</span>');
    if (ov) meta.push('<span class="wt-warn" title="Files another worktree also changes">' + ICON.warn + ov + '</span>');

    html += '<div class="wt-card' + (open ? ' open' : '') + '" data-wt="' + esc(w.name) + '">' +
      '<div class="wt-head" data-toggle="' + esc(w.name) + '" title="' + esc(w.path) + '">' + ICON.chevron +
      '<span class="wt-icon">' + ICON.branch + '</span>' +
      '<span class="wt-name">' + esc(w.name) + '</span>' +
      (w.branch && w.branch !== w.name ? '<span class="wt-branch">' + esc(w.branch) + '</span>' : (!w.branch ? '<span class="wt-branch">detached</span>' : '')) +
      '<span class="grow"></span><span class="wt-meta">' + meta.join('') + '</span>' +
      (w.files.length ? '<button class="wt-act" data-review="' + esc(w.name) + '" title="Review every change in ' + esc(w.name) + '">' + ICON.review + '</button>' : '') +
      '</div>';
    if (open) {
      html += '<div class="wt-files">';
      if (w.error) html += '<div class="wt-faint wt-err">' + esc(w.error) + '</div>';
      if (!w.files.length) html += '<div class="wt-faint wt-none">No changes yet</div>';
      for (const f of w.files.slice(0, ROW_CAP)) {
        const [dir, name] = splitPath(f.path);
        const [cls, label] = STATUS[f.status] || STATUS.M;
        html += '<div class="wt-file" data-wt="' + esc(w.name) + '" data-path="' + esc(f.path) + '" title="' + esc(f.path) + ' — ' + label + '">' +
          '<span class="wt-fname">' + esc(name) + '</span>' +
          (dir ? '<span class="wt-fdir">' + esc(dir) + '</span>' : '') +
          '<span class="grow"></span>' +
          (f.overlap?.length ? '<span class="wt-warn" title="Also changed in ' + esc(f.overlap.join(', ')) + '">' + ICON.warn + '</span>' : '') +
          '<span class="wt-st ' + cls + '">' + esc(f.status) + '</span></div>';
      }
      if (w.files.length > ROW_CAP) html += '<div class="wt-faint wt-none">' + (w.files.length - ROW_CAP) + ' more files: use Review to step through all of them</div>';
      html += '</div>';
    }
    html += '</div>';
  }
  body.innerHTML = html;
}

function filesFor(w) {
  return w.files.map(f => ({ path: '@wt/' + w.name + '/' + f.path, status: f.status === 'U' ? 'A' : f.status }));
}

function openWorktreeFile(w, path) {
  for (const el of body.querySelectorAll('.wt-file.active')) el.classList.remove('active');
  body.querySelector('.wt-file[data-wt="' + CSS.escape(w.name) + '"][data-path="' + CSS.escape(path) + '"]')?.classList.add('active');
  openFile('@wt/' + w.name + '/' + path, {
    view: 'diff',
    turnFiles: filesFor(w),
    turnLabel: w.name + (w.baseRef ? ' vs ' + w.baseRef : ''),
  });
}

function onClick(e) {
  if (!data) return;
  const find = name => data.worktrees.find(w => w.name === name);
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
    if (collapsed.has(n)) collapsed.delete(n); else collapsed.add(n);
    saveCollapsed();
    render();
  }
}
