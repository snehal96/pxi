// web/src/pxi-scmhead.js
// pxi: the top of Source Control laid out like VS Code: a SOURCE CONTROL
// header with icon actions (commit with AI, pull, push, refresh), the commit
// message always visible, one full-width primary button (Commit, or Push/Pull
// when there is nothing to commit), and a CHANGES header above the changed
// files. All git work stays px0's: these controls click px0's own buttons in
// the git panel (web/src/gitpanel.js), whose elements stay in the page.
// Top-level names are prefixed scmh: the Node fallback bundler shares one scope.
import { $, apiPostJson } from './state.js';
import { triggerRefresh } from './gitstream.js';
import { showGitError } from './gitpanel.js';

const SCMH_ICON = {
  sparkle: '<svg viewBox="0 0 16 16" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round"><path d="M6 1.5l1.2 3.3L10.5 6 7.2 7.2 6 10.5 4.8 7.2 1.5 6l3.3-1.2z"/><path d="M12 9l.6 1.7 1.7.6-1.7.6L12 13.6l-.6-1.7-1.7-.6 1.7-.6z"/></svg>',
  pull: '<svg viewBox="0 0 16 16" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><path d="M8 2v9M4.5 7.5L8 11l3.5-3.5M3 14h10"/></svg>',
  push: '<svg viewBox="0 0 16 16" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><path d="M8 14V5M4.5 8.5L8 5l3.5 3.5M3 2h10"/></svg>',
  refresh: '<svg viewBox="0 0 16 16" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9"/><path d="M13.5 2.5v3h-3"/></svg>',
  check: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M3 8.5l3 3 7-7"/></svg>',
  chev: '<svg class="scm-chev" viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M6 4l4 4-4 4"/></svg>',
  branch: '<svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"><circle cx="4.5" cy="3.5" r="1.5"/><circle cx="4.5" cy="12.5" r="1.5"/><circle cx="11.5" cy="5.5" r="1.5"/><path d="M4.5 5v6M11.5 7c0 2.5-3 3-7 4"/></svg>',
};
export const SCMH_BRANCH_ICON = SCMH_ICON.branch;

const SCMH_CHANGES_KEY = 'pxi.scm.changesCollapsed';

let scmhHead = null;       // SOURCE CONTROL header
let scmhCommit = null;     // primary button
let scmhChanges = null;    // CHANGES header
let scmhPopover = null;    // commit-with-AI options (px0's agent picker, moved here)
let scmhWatch = null;
let scmhKeepOpen = null;   // keeps px0's message box open while Source Control shows

// px0's own state, read from the elements gitpanel.js keeps up to date.
function scmhState() {
  const counts = ($('#git-counts')?.textContent || '').match(/(\d+)\s*\/\s*(\d+)/);
  const push = $('#git-push');
  const pull = $('#git-pull');
  const num = el => Number((el?.textContent || '').match(/\((\d+)\)/)?.[1] || 0);
  return {
    staged: counts ? Number(counts[1]) : 0,
    changed: counts ? Number(counts[2]) : 0,
    canCommit: !!$('#git-commit') && !$('#git-commit').disabled,
    committing: /Committing/.test($('#git-commit')?.textContent || ''),
    ahead: num(push),
    forcePush: /Force/.test(push?.textContent || ''),
    behind: num(pull),
    pushDisabled: !!push?.disabled,
    pullDisabled: !!pull?.disabled,
    branch: ($('#git-branch')?.textContent || '').trim(),
  };
}

function scmhSync() {
  if (!scmhHead) return;
  const st = scmhState();
  const ta = $('#git-commit-msg');
  if (ta) ta.placeholder = 'Message (⌘⏎ to commit' + (st.branch ? " on '" + st.branch.replace(/ \(PR review\)$/, '') + "'" : '') + ')';

  const pushBtn = scmhHead.querySelector('[data-scmh="push"]');
  pushBtn.disabled = st.pushDisabled;
  pushBtn.title = $('#git-push')?.title || 'Push';
  pushBtn.querySelector('.scmh-n').textContent = st.ahead ? String(st.ahead) : '';
  const pullBtn = scmhHead.querySelector('[data-scmh="pull"]');
  pullBtn.disabled = st.pullDisabled;
  pullBtn.title = $('#git-pull')?.title || 'Pull';
  pullBtn.querySelector('.scmh-n').textContent = st.behind ? String(st.behind) : '';

  // Primary button: Commit when there is something to commit, otherwise the
  // next step for the branch (push what is ahead, pull what is behind).
  let label, act, enabled;
  if (st.committing) { label = 'Committing…'; act = ''; enabled = false; }
  else if (st.changed > 0) { label = SCMH_ICON.check + '<span>Commit</span>'; act = 'commit'; enabled = st.canCommit; }
  else if (st.ahead > 0) { label = SCMH_ICON.push + '<span>' + (st.forcePush ? 'Force-push' : 'Push') + ' ↑' + st.ahead + '</span>'; act = 'push'; enabled = !st.pushDisabled; }
  else if (st.behind > 0) { label = SCMH_ICON.pull + '<span>Pull ↓' + st.behind + '</span>'; act = 'pull'; enabled = !st.pullDisabled; }
  else { label = SCMH_ICON.check + '<span>Commit</span>'; act = 'commit'; enabled = false; }
  if (scmhCommit.dataset.label !== label) { scmhCommit.innerHTML = label; scmhCommit.dataset.label = label; }
  scmhCommit.dataset.act = act;
  scmhCommit.disabled = !enabled;
  scmhCommit.title = act === 'commit' ? ($('#git-commit')?.title || 'Commit') + ' (⌘⏎)' : act === 'push' ? pushBtn.title : act === 'pull' ? pullBtn.title : '';

  // CHANGES header, only when there are changes (as in VS Code).
  scmhChanges.hidden = st.changed === 0;
  scmhChanges.querySelector('.scm-sec-count').textContent = st.changed ? String(st.changed) : '';
}

function scmhClick(id) {
  const b = $('#' + id);
  if (b && !b.disabled) b.click();
}

async function scmhGenerate() {
  // VS Code writes the message from every change; px0 writes it from the
  // staged ones, so stage everything first when nothing is staged yet.
  const st = scmhState();
  if (!st.changed) return;
  if (!st.staged) {
    try {
      await apiPostJson('/api/git/stage', { path: '.' });
      await triggerRefresh();
    } catch (e) {
      showGitError('Could not stage changes', e.message);
      return;
    }
  }
  $('#git-generate-link')?.click();
}

function scmhTogglePopover(force) {
  const open = force ?? scmhPopover.hidden;
  scmhPopover.hidden = !open;
}

function scmhOnClick(e) {
  const b = e.target.closest('[data-scmh]');
  if (!b || b.disabled) return;
  const a = b.dataset.scmh;
  if (a === 'ai') scmhTogglePopover();
  if (a === 'pull') scmhClick('git-pull');
  if (a === 'push') scmhClick('git-push');
  if (a === 'refresh') { triggerRefresh(); document.dispatchEvent(new CustomEvent('pxi:scm-refresh')); }
  if (a === 'generate') scmhGenerate();
  if (a === 'instructions') { scmhTogglePopover(false); $('#git-instructions-link')?.click(); }
}

function scmhBuild() {
  const side = $('#side');
  const panelBody = $('#git-panel-body');
  const section = $('#git-commit-section');
  const ta = $('#git-commit-msg');
  if (!side || !panelBody || !section || !ta) return false;

  scmhHead = document.createElement('header');
  scmhHead.id = 'scmh-head';
  scmhHead.className = 'panel-head scmh-head';
  scmhHead.innerHTML = '<span class="scmh-title">Source Control</span><span class="scm-grow"></span>' +
    '<button class="scmh-btn" data-scmh="ai" title="Commit with AI: agent, model and instructions">' + SCMH_ICON.sparkle + '</button>' +
    '<button class="scmh-btn" data-scmh="pull" title="Pull">' + SCMH_ICON.pull + '<span class="scmh-n"></span></button>' +
    '<button class="scmh-btn" data-scmh="push" title="Push">' + SCMH_ICON.push + '<span class="scmh-n"></span></button>' +
    '<button class="scmh-btn" data-scmh="refresh" title="Refresh">' + SCMH_ICON.refresh + '</button>';
  side.insertBefore(scmhHead, side.firstChild);

  // Commit with AI: px0's agent picker (harness, model, Stage all + Commit
  // with AI) moves into a small popover under the sparkle.
  scmhPopover = document.createElement('div');
  scmhPopover.id = 'scmh-popover';
  scmhPopover.hidden = true;
  scmhPopover.innerHTML = '<div class="scmh-pop-title">Commit with AI</div>';
  const picker = section.querySelector('.git-agent-picker');
  if (picker) scmhPopover.append(picker);
  const instr = document.createElement('button');
  instr.className = 'scmh-link';
  instr.dataset.scmh = 'instructions';
  instr.textContent = 'Commit message instructions…';
  scmhPopover.append(instr);
  scmhHead.after(scmhPopover);
  // Close the popover when its AI button starts (px0 shows progress there).
  picker?.querySelector('#git-generate-msg')?.addEventListener('click', () => setTimeout(() => scmhTogglePopover(false), 150));

  // Message box with a sparkle inside, like VS Code's.
  const wrap = document.createElement('div');
  wrap.className = 'scmh-msg';
  ta.before(wrap);
  wrap.append(ta);
  const gen = document.createElement('button');
  gen.className = 'scmh-gen';
  gen.dataset.scmh = 'generate';
  gen.title = 'Write the message with your coding agent';
  gen.innerHTML = SCMH_ICON.sparkle;
  wrap.append(gen);
  ta.rows = 2;

  scmhCommit = document.createElement('button');
  scmhCommit.id = 'scmh-commit';
  scmhCommit.className = 'scmh-commit';
  scmhCommit.addEventListener('click', () => {
    const act = scmhCommit.dataset.act;
    if (act === 'commit') { ta.hidden = false; scmhClick('git-commit'); }
    if (act === 'push') scmhClick('git-push');
    if (act === 'pull') scmhClick('git-pull');
  });
  section.after(scmhCommit);

  scmhChanges = document.createElement('header');
  scmhChanges.id = 'scmh-changes';
  scmhChanges.className = 'scm-sec-head scmh-changes';
  scmhChanges.innerHTML = SCMH_ICON.chev + '<span class="scm-sec-title">Changes</span><span class="scm-sec-count"></span>';
  scmhChanges.addEventListener('click', () => {
    const c = !document.body.classList.contains('pxi-changes-collapsed');
    document.body.classList.toggle('pxi-changes-collapsed', c);
    try { localStorage.setItem(SCMH_CHANGES_KEY, c ? '1' : ''); } catch {}
  });
  try { document.body.classList.toggle('pxi-changes-collapsed', localStorage.getItem(SCMH_CHANGES_KEY) === '1'); } catch {}
  side.insertBefore(scmhChanges, $('#tree'));

  side.addEventListener('click', e => { if (e.target.closest('#scmh-head, #scmh-popover, .scmh-msg')) scmhOnClick(e); });
  document.addEventListener('mousedown', e => {
    if (scmhPopover && !scmhPopover.hidden && !e.target.closest('#scmh-popover, [data-scmh="ai"]')) scmhTogglePopover(false);
  });
  return true;
}

/** Shows or hides the VS Code-style top of Source Control; built on first show. */
export function setScmHeadVisible(on) {
  if (on && !scmhHead && !scmhBuild()) return;
  if (!scmhHead) return;
  const ta = $('#git-commit-msg');
  if (on) {
    // px0 collapses the message box behind a link and after each commit;
    // Source Control keeps it open, as VS Code does.
    if (ta) ta.hidden = false;
    if (ta && !scmhKeepOpen) {
      scmhKeepOpen = new MutationObserver(() => { if (ta.hidden) ta.hidden = false; });
      scmhKeepOpen.observe(ta, { attributes: true, attributeFilter: ['hidden'] });
    }
    if (!scmhWatch) {
      scmhWatch = new MutationObserver(scmhSync);
      for (const id of ['git-commit', 'git-push', 'git-pull', 'git-counts', 'git-branch']) {
        const el = $('#' + id);
        if (el) scmhWatch.observe(el, { attributes: true, childList: true, characterData: true, subtree: true });
      }
    }
    scmhSync();
  } else {
    scmhKeepOpen?.disconnect();
    scmhKeepOpen = null;
    scmhWatch?.disconnect();
    scmhWatch = null;
    if (scmhPopover) scmhPopover.hidden = true;
  }
}
