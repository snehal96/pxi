// web/src/pxi-scm.js
// pxi: VS Code-style collapsible sections under the commit box in Source
// Control: History, Branches, Stashes, Remotes & Tags. Server: fork_git.go
// (/api/pxi/*), plus px0's /api/git/log and /api/commitfiles for commits.
// Top-level names are prefixed scm: the Node fallback bundler shares one scope.
import { esc, api, apiPostJson } from './state.js';
import { openFile } from './tabs.js';
import { loadCommitFiles, commitFileRows, COMMIT_FILES_PAGE } from './commitfiles.js';
import { showToast } from './ui.js';

const SCM_COLLAPSED_KEY = 'pxi.scm.collapsed';
const SCM_POLL_MS = 8000;
const SCM_SECTIONS = [
  { id: 'history', title: 'History' },
  { id: 'branches', title: 'Branches' },
  { id: 'stashes', title: 'Stashes' },
  { id: 'remotes', title: 'Remotes & Tags' },
];

const SCM_ICON = {
  chev: '<svg class="scm-chev" viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M6 4l4 4-4 4"/></svg>',
  commit: '<svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.4"><circle cx="8" cy="8" r="2.6"/><path d="M8 1v4.4M8 10.6V15"/></svg>',
  branch: '<svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"><circle cx="4.5" cy="3.5" r="1.5"/><circle cx="4.5" cy="12.5" r="1.5"/><circle cx="11.5" cy="5.5" r="1.5"/><path d="M4.5 5v6M11.5 7c0 2.5-3 3-7 4"/></svg>',
  stash: '<svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round"><path d="M2 5.5h12v8H2z"/><path d="M3.5 3h9l1.5 2.5H2z"/><path d="M6 8.5h4"/></svg>',
  tag: '<svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round"><path d="M1.5 2.5v5l7 7 6-6-7-7h-5z"/><circle cx="5" cy="5" r="1"/></svg>',
  remote: '<svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.3"><circle cx="8" cy="8" r="6"/><path d="M2 8h12M8 2c2 2 2 10 0 12M8 2c-2 2-2 10 0 12"/></svg>',
  plus: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"><path d="M8 3v10M3 8h10"/></svg>',
  stashAll: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round"><path d="M2 6.5h12v7H2z"/><path d="M8 1.5v3.5M6.3 3.3L8 5l1.7-1.7"/><path d="M12.5 1.5v3M11 3h3"/></svg>',
  fetch: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><path d="M8 2v9M4.5 7.5L8 11l3.5-3.5M3 14h10"/></svg>',
  refresh: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9"/><path d="M13.5 2.5v3h-3"/></svg>',
  apply: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="M3 8.5l3 3 7-7"/></svg>',
  pop: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="M8 13V4M4.5 7.5L8 4l3.5 3.5"/></svg>',
  trash: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><path d="M2.5 4h11M6 4V2.5h4V4M4 4l.7 9.5h6.6L12 4"/></svg>',
};

let scmEl = null;
let scmVisible = false;
let scmTimer = 0;
const scmState = { history: [], branches: { local: [], remote: [] }, stashes: [], remotes: { remotes: [], tags: [] } };
const scmCollapsed = new Set(scmReadCollapsed());
const scmExpanded = new Set();      // open commit / stash / tag rows, by key
const scmFiles = new Map();         // stash sha -> files
let scmShowRemoteBranches = false;
let scmSig = '';

function scmReadCollapsed() {
  try { return JSON.parse(localStorage.getItem(SCM_COLLAPSED_KEY) || '["remotes"]'); } catch { return ['remotes']; }
}

function scmSaveCollapsed() {
  try { localStorage.setItem(SCM_COLLAPSED_KEY, JSON.stringify([...scmCollapsed])); } catch {}
}

function scmActions(id) {
  const b = (act, icon, title) => '<button class="scm-hact" data-hact="' + act + '" title="' + title + '" aria-label="' + title + '">' + icon + '</button>';
  if (id === 'history') return b('refresh', SCM_ICON.refresh, 'Refresh');
  if (id === 'branches') return b('branch-create', SCM_ICON.plus, 'Create branch from here and switch to it');
  if (id === 'stashes') return b('stash-push', SCM_ICON.plus, 'Stash changes') + b('stash-push-u', SCM_ICON.stashAll, 'Stash changes, including untracked files');
  if (id === 'remotes') return b('fetch-all', SCM_ICON.fetch, 'Fetch all remotes');
  return '';
}

let scmHost = null;

/** Remembers where the sections go; they are built the first time Source Control opens. */
export function initScm() {
  scmHost = document.getElementById('git-panel-body');
  document.addEventListener('visibilitychange', scmSchedule);
}

function scmEnsureDom() {
  if (scmEl || !scmHost) return;
  scmEl = document.createElement('div');
  scmEl.id = 'pxi-scm';
  scmEl.innerHTML = SCM_SECTIONS.map(s =>
    '<section class="scm-sec" data-sec="' + s.id + '">' +
    '<header class="scm-sec-head" data-sec-toggle="' + s.id + '">' + SCM_ICON.chev +
    '<span class="scm-sec-title">' + s.title + '</span><span class="scm-sec-count" data-count="' + s.id + '"></span>' +
    '<span class="scm-grow"></span><span class="scm-hacts">' + scmActions(s.id) + '</span></header>' +
    '<div class="scm-sec-body" data-body="' + s.id + '"></div></section>').join('');
  scmHost.append(scmEl);
  scmApplyCollapsed();
  scmEl.addEventListener('click', scmOnClick);
}

export function setScmVisible(on) {
  if (on === scmVisible) return;
  scmVisible = on;
  if (on) {
    scmEnsureDom();
    scmRefresh();
    return;
  }
  clearTimeout(scmTimer);
  // A closed view keeps no rows in memory; the next open redraws.
  if (scmEl) for (const b of scmEl.querySelectorAll('.scm-sec-body')) b.replaceChildren();
  scmSig = '';
  scmFiles.clear();
}

function scmSchedule() {
  clearTimeout(scmTimer);
  if (scmVisible && !document.hidden) scmTimer = setTimeout(scmRefresh, SCM_POLL_MS);
}

function scmApplyCollapsed() {
  for (const sec of scmEl.querySelectorAll('.scm-sec')) sec.classList.toggle('collapsed', scmCollapsed.has(sec.dataset.sec));
}

async function scmRefresh() {
  if (!scmVisible || !scmEl) return;
  try {
    const [log, branches, stashes, remotes] = await Promise.all([
      api('/api/git/log', { limit: 50 }).catch(() => ({ commits: [] })),
      api('/api/pxi/branches').catch(() => ({ local: [], remote: [] })),
      api('/api/pxi/stashes').catch(() => ({ stashes: [] })),
      api('/api/pxi/remotes').catch(() => ({ remotes: [], tags: [] })),
    ]);
    scmState.history = log.commits || [];
    scmState.branches = { local: branches.local || [], remote: branches.remote || [] };
    scmState.stashes = stashes.stashes || [];
    scmState.remotes = { remotes: remotes.remotes || [], tags: remotes.tags || [] };
    const sig = JSON.stringify(scmState);
    if (sig !== scmSig) {
      scmSig = sig;
      scmRender();
    }
  } finally {
    scmSchedule();
  }
}

function scmCount(id, n) {
  const el = scmEl.querySelector('[data-count="' + id + '"]');
  if (el) el.textContent = n ? String(n) : '';
}

function scmRender() {
  const body = id => scmEl.querySelector('[data-body="' + id + '"]');
  const { history, branches, stashes, remotes } = scmState;

  scmCount('history', history.length);
  body('history').innerHTML = history.length ? history.map(c => scmCommitRow('c:' + c.full, c.full, c.subject || '(no message)', c.author + ' · ' + c.date, c.hash, SCM_ICON.commit)).join('')
    : '<div class="scm-empty">No commits yet</div>';

  scmCount('branches', branches.local.length);
  let bh = branches.local.map(b => {
    const track = (b.ahead ? '<span title="Commits to push">↑' + b.ahead + '</span>' : '') + (b.behind ? '<span title="Commits to pull">↓' + b.behind + '</span>' : '');
    const where = b.worktree ? '<span class="scm-tag" title="Checked out in ' + esc(b.worktree) + '">worktree</span>' : '';
    const del = !b.current && !b.worktree ? '<button class="scm-iact" data-branch-del="' + esc(b.name) + '" title="Delete branch (only if merged)">' + SCM_ICON.trash + '</button>' : '';
    return '<div class="scm-row scm-branch' + (b.current ? ' current' : '') + (b.worktree ? ' busy' : '') + '" data-branch="' + esc(b.name) + '" title="' +
      esc(b.name + (b.upstream ? ' → ' + b.upstream : '') + '\n' + b.subject + ' (' + b.date + ')') + (b.current ? '' : b.worktree ? '\nChecked out in another worktree' : '\nClick to switch') + '">' +
      '<span class="scm-ic">' + (b.current ? SCM_ICON.apply : SCM_ICON.branch) + '</span><span class="scm-name">' + esc(b.name) + '</span>' + where +
      '<span class="scm-grow"></span><span class="scm-meta">' + track + (b.gone ? '<span title="Upstream deleted">gone</span>' : '') + '</span>' + del + '</div>';
  }).join('');
  if (branches.remote.length) {
    bh += '<div class="scm-row scm-sub" data-remote-toggle="1">' + SCM_ICON.chev.replace('scm-chev', 'scm-chev' + (scmShowRemoteBranches ? ' open' : '')) +
      '<span class="scm-name">Remote branches</span><span class="scm-sec-count">' + branches.remote.length + '</span></div>';
    if (scmShowRemoteBranches) {
      bh += branches.remote.map(b => '<div class="scm-row scm-branch remote" data-track="' + esc(b.name) + '" title="' + esc(b.name + '\n' + b.subject + ' (' + b.date + ')\nClick to check out') + '">' +
        '<span class="scm-ic">' + SCM_ICON.remote + '</span><span class="scm-name">' + esc(b.name) + '</span><span class="scm-grow"></span><span class="scm-meta">' + esc(b.date) + '</span></div>').join('');
    }
  }
  body('branches').innerHTML = bh || '<div class="scm-empty">No branches</div>';

  scmCount('stashes', stashes.length);
  body('stashes').innerHTML = stashes.length ? stashes.map(s => {
    const key = 's:' + s.sha;
    const open = scmExpanded.has(key);
    const acts = '<span class="scm-iacts">' +
      '<button class="scm-iact" data-stash-act="apply" data-ref="' + esc(s.ref) + '" title="Apply (keep the stash)">' + SCM_ICON.apply + '</button>' +
      '<button class="scm-iact" data-stash-act="pop" data-ref="' + esc(s.ref) + '" title="Pop (apply and remove)">' + SCM_ICON.pop + '</button>' +
      '<button class="scm-iact" data-stash-act="drop" data-ref="' + esc(s.ref) + '" title="Drop">' + SCM_ICON.trash + '</button></span>';
    return '<div class="scm-item' + (open ? ' open' : '') + '" data-key="' + key + '" data-stash="' + esc(s.sha) + '">' +
      '<div class="scm-row" data-expand="' + key + '" title="' + esc(s.ref + ' · ' + s.message + '\non ' + (s.branch || '?') + ', ' + s.date) + '">' + SCM_ICON.chev +
      '<span class="scm-ic">' + SCM_ICON.stash + '</span><span class="scm-name">' + esc(s.message) + '</span>' +
      '<span class="scm-grow"></span><span class="scm-meta">' + esc(s.date) + '</span>' + acts + '</div>' +
      '<div class="scm-files">' + (open ? scmStashFilesHtml(s.sha) : '') + '</div></div>';
  }).join('') : '<div class="scm-empty">No stashes</div>';

  scmCount('remotes', remotes.remotes.length + remotes.tags.length);
  let rh = remotes.remotes.map(r => '<div class="scm-row" title="' + esc(r.url) + '"><span class="scm-ic">' + SCM_ICON.remote + '</span><span class="scm-name">' + esc(r.name) + '</span>' +
    '<span class="scm-dim">' + esc(r.url) + '</span><span class="scm-grow"></span><button class="scm-iact" data-fetch="' + esc(r.name) + '" title="Fetch ' + esc(r.name) + '">' + SCM_ICON.fetch + '</button></div>').join('');
  if (!remotes.remotes.length) rh += '<div class="scm-empty">No remotes</div>';
  if (remotes.tags.length) {
    rh += '<div class="scm-label">Tags</div>' + remotes.tags.map(t => scmCommitRow('t:' + t.name, t.sha, t.name, t.date + (t.subject ? ' · ' + t.subject : ''), '', SCM_ICON.tag)).join('');
  }
  body('remotes').innerHTML = rh;

  // Re-open expanded commits/tags (their files load lazily).
  for (const el of scmEl.querySelectorAll('.scm-item.open[data-sha]')) scmFillCommitFiles(el);
}

function scmCommitRow(key, sha, title, meta, hash, icon) {
  const open = scmExpanded.has(key);
  return '<div class="scm-item' + (open ? ' open' : '') + '" data-key="' + esc(key) + '" data-sha="' + esc(sha) + '">' +
    '<div class="scm-row" data-expand="' + esc(key) + '" title="' + esc(title + '\n' + meta) + '">' + SCM_ICON.chev +
    '<span class="scm-ic">' + icon + '</span><span class="scm-name">' + esc(title) + '</span>' +
    '<span class="scm-grow"></span><span class="scm-meta">' + esc(hash || meta.split(' · ')[0]) + '</span></div>' +
    '<div class="scm-files"></div></div>';
}

async function scmFillCommitFiles(el) {
  const box = el.querySelector('.scm-files');
  if (!box || box.childElementCount) return;
  box.innerHTML = '<div class="scm-empty">Loading…</div>';
  try {
    const files = await loadCommitFiles(el.dataset.sha);
    box.innerHTML = files.length ? commitFileRows(files, 0, COMMIT_FILES_PAGE) : '<div class="scm-empty">No file changes</div>';
  } catch (e) {
    box.innerHTML = '<div class="scm-empty">' + esc(e.message || 'Could not load') + '</div>';
  }
}

function scmStashFilesHtml(sha) {
  const files = scmFiles.get(sha);
  if (!files) return '<div class="scm-empty">Loading…</div>';
  if (!files.length) return '<div class="scm-empty">No file changes</div>';
  const label = { M: ['git-M', 'Modified'], A: ['git-A', 'Added'], D: ['git-D', 'Deleted'], U: ['git-untracked', 'Untracked'] };
  return files.map((f, i) => {
    const [cls, what] = label[f.status] || label.M;
    const name = f.path.split('/').pop();
    const dir = f.path.slice(0, f.path.length - name.length).replace(/\/$/, '');
    return '<div class="up-file" data-stash-file="' + i + '" title="' + esc(f.path) + ' — ' + what + ' in this stash">' +
      '<span class="up-file-name">' + esc(name) + '</span>' + (dir ? '<span class="up-file-dir">' + esc(dir) + '</span>' : '') +
      '<span class="gs ' + cls + '">' + esc(f.status) + '</span></div>';
  }).join('');
}

async function scmLoadStash(el) {
  const sha = el.dataset.stash;
  if (!scmFiles.has(sha)) {
    try {
      const j = await api('/api/pxi/stash-files', { sha });
      scmFiles.set(sha, j.files || []);
    } catch (e) {
      scmFiles.set(sha, []);
      showToast('!', 'Could not read stash: ' + e.message);
    }
  }
  const box = el.querySelector('.scm-files');
  if (box) box.innerHTML = scmStashFilesHtml(sha);
}

async function scmPost(path, body, done) {
  try {
    const j = await apiPostJson(path, body);
    if (done) showToast('✓', done);
    scmSig = '';
    await scmRefresh();
    return j;
  } catch (e) {
    showToast('!', e.message || String(e), 6000);
    return null;
  }
}

function scmOnClick(e) {
  const t = e.target;
  const hact = t.closest('[data-hact]');
  if (hact) {
    e.stopPropagation();
    const a = hact.dataset.hact;
    if (a === 'refresh') { scmSig = ''; scmRefresh(); }
    if (a === 'branch-create') {
      const name = prompt('New branch name (created from the current commit):');
      if (name) scmPost('/api/pxi/branch', { action: 'create', name: name.trim() }, 'Switched to new branch ' + name.trim());
    }
    if (a === 'stash-push' || a === 'stash-push-u') {
      const message = prompt('Stash message (optional):', '');
      if (message !== null) scmPost('/api/pxi/stash', { action: 'push', message, untracked: a === 'stash-push-u' }, 'Changes stashed');
    }
    if (a === 'fetch-all') { showToast('…', 'Fetching all remotes'); scmPost('/api/pxi/fetch', {}, 'Fetched'); }
    return;
  }
  const secHead = t.closest('[data-sec-toggle]');
  if (secHead) {
    const id = secHead.dataset.secToggle;
    if (scmCollapsed.has(id)) scmCollapsed.delete(id); else scmCollapsed.add(id);
    scmSaveCollapsed();
    scmApplyCollapsed();
    return;
  }
  const stashAct = t.closest('[data-stash-act]');
  if (stashAct) {
    e.stopPropagation();
    const { stashAct: act, ref } = stashAct.dataset;
    if (act === 'drop' && !confirm('Drop ' + ref + '? Its changes are lost.')) return;
    scmPost('/api/pxi/stash', { action: act, ref }, act === 'drop' ? 'Stash dropped' : act === 'pop' ? 'Stash applied and removed' : 'Stash applied');
    return;
  }
  const del = t.closest('[data-branch-del]');
  if (del) {
    e.stopPropagation();
    const name = del.dataset.branchDel;
    if (confirm('Delete branch ' + name + '? Git refuses if it has commits that are not merged.')) scmPost('/api/pxi/branch', { action: 'delete', name }, 'Deleted ' + name);
    return;
  }
  const fetchBtn = t.closest('[data-fetch]');
  if (fetchBtn) { e.stopPropagation(); scmPost('/api/pxi/fetch', { remote: fetchBtn.dataset.fetch }, 'Fetched ' + fetchBtn.dataset.fetch); return; }
  if (t.closest('[data-remote-toggle]')) { scmShowRemoteBranches = !scmShowRemoteBranches; scmRender(); return; }
  const track = t.closest('[data-track]');
  if (track) {
    const full = track.dataset.track;
    const short = full.slice(full.indexOf('/') + 1);
    const local = scmState.branches.local.find(b => b.name === short);
    if (local) scmPost('/api/pxi/branch', { action: 'switch', name: short }, 'Switched to ' + short);
    else scmPost('/api/pxi/branch', { action: 'track', name: full }, 'Checked out ' + short);
    return;
  }
  const br = t.closest('[data-branch]');
  if (br) {
    const b = scmState.branches.local.find(x => x.name === br.dataset.branch);
    if (b && !b.current && !b.worktree) scmPost('/api/pxi/branch', { action: 'switch', name: b.name }, 'Switched to ' + b.name);
    return;
  }
  const stashFile = t.closest('[data-stash-file]');
  if (stashFile) {
    const sha = stashFile.closest('[data-stash]').dataset.stash;
    const files = scmFiles.get(sha) || [];
    const f = files[+stashFile.dataset.stashFile];
    if (!f) return;
    const tracked = files.filter(x => x.status !== 'U').map(x => ({ path: x.path, status: x.status }));
    const ref = scmState.stashes.find(s => s.sha === sha)?.ref || 'stash';
    openFile(f.path, f.status === 'U'
      ? { ref: f.ref, base: f.base, view: 'diff', turnLabel: ref + ' (untracked)' }
      : { ref: f.ref, base: f.base, view: 'diff', turnFiles: tracked, turnLabel: ref });
    return;
  }
  const file = t.closest('.up-file');
  if (file) {
    const sha = file.closest('[data-sha]')?.dataset.sha;
    if (sha) openFile(file.dataset.path, { ref: sha, view: 'diff' });
    return;
  }
  const exp = t.closest('[data-expand]');
  if (exp) {
    const item = exp.closest('.scm-item');
    const key = item.dataset.key;
    const open = !scmExpanded.has(key);
    if (open) scmExpanded.add(key); else scmExpanded.delete(key);
    item.classList.toggle('open', open);
    if (!open) { item.querySelector('.scm-files').innerHTML = ''; return; }
    if (item.dataset.stash) scmLoadStash(item);
    else scmFillCommitFiles(item);
  }
}
