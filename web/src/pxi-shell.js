// web/src/pxi-shell.js
// pxi: a VS Code-style activity bar on the far left that switches the sidebar
// between Explorer, Source Control and Worktrees, and opens Search and Threads
// in the inspector. It drives px0's own sidebar modes (tree.js) rather than
// replacing them, so upstream changes to those keep working. Styles live in
// web/pxi/shell.css.
import { $, S, MOD } from './state.js';
import { layout, render } from './renderer.js';
import { setSidebarMode, hasGitView } from './tree.js';
import { showRightInspector } from './inspector.js';
import { openSettings } from './settings.js';
import { on } from './bus.js';
import { initWorktrees, setWorktreesVisible } from './pxi-worktrees.js';

const VIEW_KEY = 'pxi.sideView';

const ICONS = {
  explorer: '<path d="M9 3.5H5.5a1.5 1.5 0 0 0-1.5 1.5v14a1.5 1.5 0 0 0 1.5 1.5h9A1.5 1.5 0 0 0 16 19v-1.5"/><path d="M13 3.5h3.5L20 7v9a1.5 1.5 0 0 1-1.5 1.5h-6A1.5 1.5 0 0 1 11 16V5a1.5 1.5 0 0 1 1.5-1.5H13z"/><path d="M16.5 3.5V7H20"/>',
  scm: '<circle cx="6.5" cy="5.5" r="2"/><circle cx="6.5" cy="18.5" r="2"/><circle cx="17.5" cy="8.5" r="2"/><path d="M6.5 7.5v9M17.5 10.5c0 4-4.5 4.5-11 6"/>',
  worktrees: '<circle cx="5" cy="5" r="2"/><circle cx="5" cy="19" r="2"/><circle cx="19" cy="7" r="2"/><circle cx="19" cy="15" r="2"/><path d="M5 7v10M17 7c-6 0-8 1-10.2 3.5M17 15c-6 0-8-1-10.2-3.5"/>',
  search: '<circle cx="10.5" cy="10.5" r="6"/><path d="M15 15l5.5 5.5"/>',
  threads: '<path d="M4 5.5A1.5 1.5 0 0 1 5.5 4h13A1.5 1.5 0 0 1 20 5.5v9a1.5 1.5 0 0 1-1.5 1.5H10l-4.5 4v-4h0A1.5 1.5 0 0 1 4 14.5z"/><path d="M8 9h8M8 12h5"/>',
  home: '<path d="M3.5 11L12 4l8.5 7"/><path d="M6 9.5V20h4.5v-5.5h3V20H18V9.5"/>',
  settings: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>',
};

const mod = MOD === 'metaKey' ? '⌘' : 'Ctrl+';
const ITEMS = [
  { id: 'explorer', title: 'Explorer', view: true },
  { id: 'scm', title: 'Source Control', view: true },
  { id: 'worktrees', title: 'Worktrees', view: true },
  { id: 'search', title: `Search (${mod}⇧F)` },
  { id: 'threads', title: 'Threads' },
];
const BOTTOM = [
  { id: 'home', title: 'Workbench' },
  { id: 'settings', title: `Settings (${mod},)` },
];

let bar = null;
let current = 'explorer';

function icon(id) {
  return '<svg viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">' + ICONS[id] + '</svg>';
}

function button(it) {
  return '<button class="ab-item" data-ab="' + it.id + '" title="' + it.title + '" aria-label="' + it.title + '">' +
    icon(it.id) + '<span class="ab-badge" hidden></span></button>';
}

function sideHidden() { return document.body.classList.contains('side-hidden'); }

function setSideHidden(hidden) {
  document.body.classList.toggle('side-hidden', hidden);
  layout();
  render();
}

function markActive() {
  for (const b of bar.querySelectorAll('.ab-item[data-ab]')) {
    const isView = ITEMS.find(i => i.id === b.dataset.ab)?.view;
    b.classList.toggle('active', !!isView && !sideHidden() && b.dataset.ab === current);
  }
}

async function showView(view, { toggle = false } = {}) {
  if (toggle && view === current && !sideHidden()) {
    setSideHidden(true);
    markActive();
    return;
  }
  current = view;
  try { localStorage.setItem(VIEW_KEY, view); } catch {}
  for (const v of ['explorer', 'scm', 'worktrees']) document.body.classList.toggle('pxi-view-' + v, view === v);
  setWorktreesVisible(view === 'worktrees');
  if (sideHidden()) setSideHidden(false);
  if (view === 'explorer') await setSidebarMode('files');
  if (view === 'scm') await setSidebarMode(hasGitView() ? 'git' : 'files');
  layout();
  render();
  markActive();
}

function onBarClick(e) {
  const b = e.target.closest('[data-ab]');
  if (!b) return;
  const id = b.dataset.ab;
  if (ITEMS.find(i => i.id === id)?.view) return showView(id, { toggle: true });
  if (id === 'search') {
    showRightInspector('search');
    setTimeout(() => $('#pane-right-search input, #right-search-input, #search-q')?.focus(), 0);
  } else if (id === 'threads') {
    showRightInspector('threads');
  } else if (id === 'settings') {
    openSettings();
  } else if (id === 'home' && S.meta?.workbench) {
    location.href = S.meta.workbench;
  }
}

function setBadge(id, text, warn = false) {
  const el = bar?.querySelector('[data-ab="' + id + '"] .ab-badge');
  if (!el) return;
  el.hidden = !text;
  el.textContent = text || '';
  el.classList.toggle('warn', warn);
}

/** Call once the workspace metadata (S.meta) has loaded. */
export function initShell() {
  const app = $('#app');
  const side = $('#side');
  if (!app || !side || bar) return;
  document.body.classList.add('pxi-shell');

  bar = document.createElement('nav');
  bar.id = 'activitybar';
  bar.setAttribute('aria-label', 'Views');
  bar.innerHTML = '<div class="ab-top">' + ITEMS.map(button).join('') + '</div>' +
    '<div class="ab-bottom">' + BOTTOM.filter(b => b.id !== 'home' || S.meta?.workbench).map(button).join('') + '</div>';
  app.insertBefore(bar, app.firstChild);
  bar.addEventListener('click', onBarClick);

  initWorktrees(side, (count, overlaps) => {
    setBadge('worktrees', overlaps ? String(overlaps) : (count ? String(count) : ''), overlaps > 0);
    const b = bar.querySelector('[data-ab="worktrees"]');
    if (b) b.title = 'Worktrees' + (count ? ` (${count})` : '') + (overlaps ? `: ${overlaps} file${overlaps > 1 ? 's' : ''} changed in more than one` : '');
  });

  // px0's own controls (the tree header toggles, reindex) switch modes too.
  on('sidebar:mode', ({ mode }) => {
    if (current === 'worktrees') return;
    current = mode === 'git' ? 'scm' : 'explorer';
    for (const v of ['explorer', 'scm']) document.body.classList.toggle('pxi-view-' + v, current === v);
    markActive();
  });

  // VS Code's status bar runs the full width of the window, under every column.
  const status = $('#status');
  if (status) document.body.append(status);
  new MutationObserver(markActive).observe(document.body, { attributes: true, attributeFilter: ['class'] });

  // Tabs for files in other worktrees say which worktree they belong to.
  const tabs = $('#tabs');
  if (tabs) {
    const label = () => {
      // A file from another worktree is read-only here: no stage, discard, edit.
      document.body.classList.toggle('pxi-wt-tab', !!tabs.querySelector('.tab.active[title^="@wt/"]'));
      for (const t of tabs.querySelectorAll('.tab[title^="@wt/"]')) {
        if (t.querySelector('.tab-wt')) continue;
        const name = t.title.split('/')[1];
        const tag = document.createElement('span');
        tag.className = 'tab-wt';
        tag.textContent = name;
        t.querySelector('.tn')?.after(tag);
      }
    };
    new MutationObserver(label).observe(tabs, { childList: true });
    label();
  }

  let saved = 'explorer';
  try { saved = localStorage.getItem(VIEW_KEY) || 'explorer'; } catch {}
  if (saved === 'worktrees') showView('worktrees');
  else {
    document.body.classList.add('pxi-view-' + current);
    markActive();
  }
}
