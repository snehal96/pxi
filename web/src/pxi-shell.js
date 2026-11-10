// web/src/pxi-shell.js
// pxi: a VS Code-style activity bar on the far left that switches the sidebar
// between Explorer, Source Control and Worktrees, and opens Search and Threads
// in the inspector. It drives px0's own sidebar modes (tree.js) rather than
// replacing them, so upstream changes to those keep working. Styles live in
// web/pxi/shell.css.
import { $, S, MOD, doc_ } from './state.js';
import { layout, render } from './renderer.js';
import { setSidebarMode, hasGitView } from './tree.js';
import { showRightInspector, hideRightInspector } from './inspector.js';
import { showThreadsSidebar, toggleThreadsSidebar } from './thread.js';
import { openSettings } from './settings.js';
import { on } from './bus.js';
import { setDiffMode, layoutPref } from './diff.js';
import { initWorktrees, setWorktreesVisible } from './pxi-worktrees.js';
import { initScm, setScmVisible, scmRevealBranches } from './pxi-scm.js';
import { SCMH_BRANCH_ICON } from './pxi-scmhead.js';
import { initTerminal, toggleTerminal, TERM_STATUS_ICON } from './pxi-term.js';
import { initPxiPalette } from './pxi-palette.js';
import { setTipsPermanentlyHidden } from './tips.js';
import { initClaude, openClaude } from './pxi-claude.js';

const VIEW_KEY = 'pxi.sideView';

const ICONS = {
  explorer: '<path d="M9 3.5H5.5a1.5 1.5 0 0 0-1.5 1.5v14a1.5 1.5 0 0 0 1.5 1.5h9A1.5 1.5 0 0 0 16 19v-1.5"/><path d="M13 3.5h3.5L20 7v9a1.5 1.5 0 0 1-1.5 1.5h-6A1.5 1.5 0 0 1 11 16V5a1.5 1.5 0 0 1 1.5-1.5H13z"/><path d="M16.5 3.5V7H20"/>',
  scm: '<circle cx="6.5" cy="5.5" r="2"/><circle cx="6.5" cy="18.5" r="2"/><circle cx="17.5" cy="8.5" r="2"/><path d="M6.5 7.5v9M17.5 10.5c0 4-4.5 4.5-11 6"/>',
  worktrees: '<circle cx="5" cy="5" r="2"/><circle cx="5" cy="19" r="2"/><circle cx="19" cy="7" r="2"/><circle cx="19" cy="15" r="2"/><path d="M5 7v10M17 7c-6 0-8 1-10.2 3.5M17 15c-6 0-8-1-10.2-3.5"/>',
  refs: '<path d="M4 6h9M4 12h6M4 18h9"/><path d="M16 9l4 3-4 3"/><path d="M20 12h-6"/>',
  symbols: '<path d="M8 4H6.5A1.5 1.5 0 0 0 5 5.5v4L3.5 12 5 14.5v4A1.5 1.5 0 0 0 6.5 20H8M16 4h1.5A1.5 1.5 0 0 1 19 5.5v4l1.5 2.5-1.5 2.5v4a1.5 1.5 0 0 1-1.5 1.5H16"/>',
  calls: '<rect x="3" y="4" width="7" height="5" rx="1"/><rect x="14" y="10" width="7" height="5" rx="1"/><rect x="14" y="17" width="7" height="4" rx="1"/><path d="M6.5 9v10H14M6.5 12.5H14"/>',
  tips: '<path d="M9 18h6M10 21h4"/><path d="M12 3a6 6 0 0 0-3.5 10.9c.6.5 1 1.2 1 2.1h5c0-.9.4-1.6 1-2.1A6 6 0 0 0 12 3z"/>',
  search: '<circle cx="10.5" cy="10.5" r="6"/><path d="M15 15l5.5 5.5"/>',
  review: '<path d="M9 5H6.5A1.5 1.5 0 0 0 5 6.5v13A1.5 1.5 0 0 0 6.5 21h11a1.5 1.5 0 0 0 1.5-1.5v-13A1.5 1.5 0 0 0 17.5 5H15"/><rect x="9" y="3" width="6" height="4" rx="1"/><path d="M8.5 12.5l1.5 1.5 3-3M8.5 17h7"/>',
  threads: '<path d="M4 5.5A1.5 1.5 0 0 1 5.5 4h13A1.5 1.5 0 0 1 20 5.5v9a1.5 1.5 0 0 1-1.5 1.5H10l-4.5 4v-4h0A1.5 1.5 0 0 1 4 14.5z"/><path d="M8 9h8M8 12h5"/>',
  claude: '<path d="M12 3.5v17M3.5 12h17M6 6l12 12M18 6L6 18"/>',
  home: '<path d="M3.5 11L12 4l8.5 7"/><path d="M6 9.5V20h4.5v-5.5h3V20H18V9.5"/>',
  settings: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>',
};

const mod = MOD === 'metaKey' ? '⌘' : 'Ctrl+';
const ITEMS = [
  { id: 'explorer', title: 'Explorer', view: true },
  { id: 'scm', title: 'Source Control', view: true },
  { id: 'worktrees', title: 'Worktrees', view: true },
  { id: 'claude', title: `Claude: sessions in this repo and its worktrees (${mod}Esc)` },
  { id: 'review', title: `AI Review: run your agent through review stages (${mod}⇧V)` },
];
// Right activity bar: px0's inspector panes, one icon each. The panel is closed
// until an icon (or a px0 shortcut such as ⇧F12 or ⌥T) opens it.
const RIGHT_ITEMS = [
  { id: 'threads', title: 'Threads: conversations with your coding agent (⌥T)' },
  { id: 'refs', title: 'References (⇧F12)' },
  { id: 'symbols', title: 'Symbols in this file' },
  { id: 'calls', title: 'Calls: callers and callees (⌥⇧H)' },
  { id: 'search', title: `Search across files (${mod}⇧F)` },
];
const RIGHT_BOTTOM = [{ id: 'tips', title: 'Tips' }];
const RIGHT_KEY = 'pxi.rightPane';

const BOTTOM = [
  { id: 'home', title: 'Workbench' },
  { id: 'settings', title: `Settings (${mod},)` },
];

let abBar = null;
let abCurrent = 'explorer';

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

function reviewOn() { return !!$('#btn-mode-review')?.classList.contains('active'); }

function markActive() {
  for (const b of abBar.querySelectorAll('.ab-item[data-ab]')) {
    const isView = ITEMS.find(i => i.id === b.dataset.ab)?.view;
    if (b.dataset.ab === 'review') { b.classList.toggle('active', reviewOn()); continue; }
    b.classList.toggle('active', !!isView && !sideHidden() && b.dataset.ab === abCurrent);
  }
}

async function showView(view, { toggle = false } = {}) {
  if (toggle && view === abCurrent && !sideHidden()) {
    setSideHidden(true);
    markActive();
    return;
  }
  abCurrent = view;
  try { localStorage.setItem(VIEW_KEY, view); } catch {}
  for (const v of ['explorer', 'scm', 'worktrees']) document.body.classList.toggle('pxi-view-' + v, view === v);
  setWorktreesVisible(view === 'worktrees');
  setScmVisible(view === 'scm');
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
  if (id === 'claude') {
    openClaude();
  } else if (id === 'review') {
    // px0's Code/Review mode switch, which pxi hides from the toolbar.
    (reviewOn() ? $('#btn-mode-code') : $('#btn-mode-review'))?.click();
  } else if (id === 'settings') {
    openSettings();
  } else if (id === 'home' && S.meta?.workbench) {
    location.href = S.meta.workbench;
  }
}

function setBadge(id, text, warn = false) {
  const el = abBar?.querySelector('[data-ab="' + id + '"] .ab-badge');
  if (!el) return;
  el.hidden = !text;
  el.textContent = text || '';
  el.classList.toggle('warn', warn);
}

// Editor toolbar: one icon toggles source/diff (px0's #diff-btn, ⌘D), and a
// second one, shown only in a diff, flips side by side / inline.
const PXI_LAYOUT_ICON = {
  split: '<svg viewBox="0 0 16 16" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.2"><rect x="1.5" y="2.5" width="13" height="11" rx="1"/><path d="M8 2.5v11"/></svg>',
  unified: '<svg viewBox="0 0 16 16" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.2"><rect x="1.5" y="2.5" width="13" height="11" rx="1"/><path d="M1.5 8h13"/></svg>',
};

function pxiCompactDiffControls() {
  const dsw = $('#diff-switch');
  const diffBtn = $('#diff-btn');
  if (!dsw || !diffBtn) return;
  const btn = document.createElement('button');
  btn.id = 'pxi-diff-layout';
  btn.type = 'button';
  dsw.append(btn);
  const sync = () => {
    const d = doc_();
    btn.hidden = !d?.diffMode;
    const unified = (d?.diffMode || layoutPref()) === 'unified';
    btn.innerHTML = unified ? PXI_LAYOUT_ICON.unified : PXI_LAYOUT_ICON.split;
    btn.title = unified ? 'Inline view (click for side by side)' : 'Side by side (click for inline)';
    btn.setAttribute('aria-label', btn.title);
  };
  btn.addEventListener('click', e => {
    e.stopPropagation();
    const d = doc_();
    if (!d?.diffMode) return;
    setDiffMode(d.diffMode === 'unified' ? 'split' : 'unified');
    sync();
  });
  new MutationObserver(sync).observe(diffBtn, { attributes: true, attributeFilter: ['class', 'title'] });
  sync();
}

let abRight = null;

// Threads has its own sidebar (px0's #threads-side); the rest are inspector panes.
const threadsOpen = () => !document.body.classList.contains('threads-hidden');
const threadsAvailable = () => !$('#btn-top-threads')?.hasAttribute('hidden');

function rightPane() {
  if (threadsOpen()) return 'threads';
  if (document.body.classList.contains('right-hidden')) return '';
  return $('.inspector-tab.active')?.dataset.itab || '';
}

function markRight() {
  if (!abRight) return;
  const open = rightPane();
  for (const b of abRight.querySelectorAll('.ab-item[data-rb]')) {
    b.classList.toggle('active', b.dataset.rb === open);
    const tab = $('.inspector-tab[data-itab="' + b.dataset.rb + '"]');
    b.hidden = b.dataset.rb === 'threads' ? !threadsAvailable() : !!tab?.hidden; // Threads needs a coding harness
  }
  try { localStorage.setItem(RIGHT_KEY, open); } catch {}
}

function initRightBar() {
  const app = $('#app');
  abRight = document.createElement('nav');
  abRight.id = 'activitybar-right';
  abRight.setAttribute('aria-label', 'Panels');
  const rb = it => '<button class="ab-item" data-rb="' + it.id + '" title="' + it.title + '" aria-label="' + it.title + '">' + icon(it.id) + '</button>';
  abRight.innerHTML = '<div class="ab-top">' + RIGHT_ITEMS.map(rb).join('') + '</div><div class="ab-bottom">' + RIGHT_BOTTOM.map(rb).join('') + '</div>';
  app.append(abRight);
  abRight.addEventListener('click', e => {
    const b = e.target.closest('[data-rb]');
    if (!b) return;
    if (b.dataset.rb === 'threads') toggleThreadsSidebar();
    else if (rightPane() === b.dataset.rb) hideRightInspector();
    else showRightInspector(b.dataset.rb);
    markRight();
  });
  // px0 opens panes itself too (⇧F12, ⌥T, ⌘⇧F, the palette).
  const watch = new MutationObserver(markRight);
  watch.observe(document.body, { attributes: true, attributeFilter: ['class'] });
  for (const t of document.querySelectorAll('.inspector-tab, #btn-top-threads')) watch.observe(t, { attributes: true, attributeFilter: ['class', 'hidden'] });

  // Closed unless it was open last time (px0 opens it on every load).
  let saved = '';
  try { saved = localStorage.getItem(RIGHT_KEY) || ''; } catch {}
  const tab = saved && $('.inspector-tab[data-itab="' + saved + '"]');
  if (saved === 'threads' && threadsAvailable()) showThreadsSidebar();
  else if (tab && !tab.hidden) showRightInspector(saved);
  else hideRightInspector();
  markRight();
}

/** Call once the workspace metadata (S.meta) has loaded. */
export function initShell() {
  const app = $('#app');
  const side = $('#side');
  if (!app || !side || abBar) return;
  document.body.classList.add('pxi-shell');
  pxiCompactDiffControls();
  // Closing the tip popup (X) means "stop popping up", remembered in settings.json
  // for every window and workspace. Tips stay in the right bar's Tips view.
  initClaude();
  $('#tip-widget-close')?.addEventListener('click', () => setTipsPermanentlyHidden(true));

  abBar = document.createElement('nav');
  abBar.id = 'activitybar';
  abBar.setAttribute('aria-label', 'Views');
  abBar.innerHTML = '<div class="ab-top">' + ITEMS.map(button).join('') + '</div>' +
    '<div class="ab-bottom">' + BOTTOM.filter(b => b.id !== 'home' || S.meta?.workbench).map(button).join('') + '</div>';
  app.insertBefore(abBar, app.firstChild);
  abBar.addEventListener('click', onBarClick);

  initScm();
  initRightBar();
  const modeBtn = $('#btn-mode-review');
  if (modeBtn) new MutationObserver(markActive).observe(modeBtn, { attributes: true, attributeFilter: ['class'] });

  initWorktrees(side, (count, overlaps) => {
    setBadge('worktrees', overlaps ? String(overlaps) : (count ? String(count) : ''), overlaps > 0);
    const b = abBar.querySelector('[data-ab="worktrees"]');
    if (b) b.title = 'Worktrees' + (count ? ` (${count})` : '') + (overlaps ? `: ${overlaps} file${overlaps > 1 ? 's' : ''} changed in more than one` : '');
  });

  // px0's own controls (the tree header toggles, reindex) switch modes too.
  // px0 switches its tree between all files and changed files by itself (on
  // load, after a reindex, when the last change is committed). The activity
  // bar's choice wins: Explorer shows all files, Source Control the changes.
  on('sidebar:mode', ({ mode }) => {
    if (abCurrent === 'explorer' && mode === 'git') setSidebarMode('files');
    else if (abCurrent === 'scm' && mode === 'files' && hasGitView()) setSidebarMode('git');
    markActive();
  });

  // VS Code's status bar runs the full width of the window, under every column.
  const status = $('#status');
  if (status) {
    document.body.append(status);
    const tb = document.createElement('button');
    tb.id = 'pxi-term-btn';
    tb.className = 'footer-btn';
    tb.title = 'Terminal (⌃`)';
    tb.innerHTML = TERM_STATUS_ICON + '<span>Terminal</span>';
    tb.addEventListener('click', toggleTerminal);
    status.prepend(tb);
    // The current branch, at the left of the status bar as in VS Code; click
    // for the Branches section of Source Control.
    const bb = document.createElement('button');
    bb.id = 'pxi-branch-btn';
    bb.className = 'footer-btn';
    bb.hidden = true;
    bb.addEventListener('click', async () => {
      await showView('scm');
      scmRevealBranches();
    });
    status.prepend(bb);
    const src = $('#git-branch');
    const showBranch = () => {
      const name = (src?.textContent || '').trim();
      bb.hidden = !name;
      bb.innerHTML = SCMH_BRANCH_ICON + '<span>' + name.replace(/[&<>"]/g, c => '&#' + c.charCodeAt(0) + ';') + '</span>';
      bb.title = name ? name + ': show branches' : '';
    };
    if (src) new MutationObserver(showBranch).observe(src, { childList: true, characterData: true, subtree: true });
    showBranch();
  }
  initTerminal();
  initPxiPalette();
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
  showView(['explorer', 'scm', 'worktrees'].includes(saved) ? saved : 'explorer');
}
