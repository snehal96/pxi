// web/src/pxi-remote.js
// pxi: the Remote view (activity bar). Browse a folder on an SSH host and read
// its files, read-only; they open in px0's viewer as "@ssh/<id>/<path>"
// (fork_remote.go). Saved remotes come back after a restart. The view builds
// its DOM when opened and empties it when closed; nothing polls.
// Styles: web/pxi/shell.css. Top-level names are prefixed rmt (Node fallback bundler).
import { $, api, apiPostJson, esc } from './state.js';
import { showToast } from './ui.js';
import { openFile } from './tabs.js';
import { fileKind } from './tree.js';

let rmtRoot = null;
let rmtBody = null;
let rmtVisible = false;
let rmtList = [];             // saved remotes
let rmtHosts = [];            // Host names from ~/.ssh/config
const rmtOpen = new Set();    // "<id>:<dir>" folders expanded, kept while pxi runs
const rmtErr = new Map();     // id -> last error

const RMT_ICON = {
  plus: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.4"><path d="M8 3v10M3 8h10"/></svg>',
  refresh: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.4"><path d="M13 8a5 5 0 1 1-1.5-3.5M13 3v2.5h-2.5"/></svg>',
  close: '<svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.4"><path d="M4 4l8 8M12 4l-8 8"/></svg>',
};

function rmtEnsureDom() {
  if (rmtRoot) return;
  rmtRoot = document.createElement('section');
  rmtRoot.id = 'rmt-view';
  rmtRoot.innerHTML =
    '<header class="panel-head rmt-head"><span>Remote (SSH)</span>' +
    '<button class="mini" id="rmt-add" title="Connect to a folder on an SSH host">' + RMT_ICON.plus + '</button>' +
    '<button class="mini" id="rmt-refresh" title="Refresh">' + RMT_ICON.refresh + '</button></header>' +
    '<div class="rmt-body" id="rmt-body"></div>';
  const side = $('#side');
  side.insertBefore(rmtRoot, side.querySelector('.side-foot'));
  rmtBody = rmtRoot.querySelector('#rmt-body');
  rmtRoot.querySelector('#rmt-add').addEventListener('click', () => rmtForm(true));
  rmtRoot.querySelector('#rmt-refresh').addEventListener('click', rmtLoad);
  rmtBody.addEventListener('click', rmtClick);
  rmtBody.addEventListener('submit', rmtSubmit);
}

export function setRemoteVisible(on) {
  if (on === rmtVisible) return;
  rmtVisible = on;
  if (on) {
    rmtEnsureDom();
    rmtLoad();
  } else if (rmtBody) {
    rmtBody.replaceChildren(); // a closed view keeps no rows
  }
}

async function rmtLoad() {
  try {
    const j = await api('/api/pxi/ssh/remotes');
    rmtList = j.remotes || [];
    rmtHosts = j.hosts || [];
  } catch (e) {
    rmtBody.innerHTML = '<div class="wt-empty">' + esc(e.message) + '</div>';
    return;
  }
  rmtDraw();
}

function rmtFormHTML() {
  return '<form class="rmt-form" id="rmt-form">' +
    '<label>Host<input name="host" list="rmt-hosts" placeholder="devbox or user@host" required autocomplete="off" spellcheck="false"></label>' +
    '<datalist id="rmt-hosts">' + rmtHosts.map(h => '<option value="' + esc(h) + '">').join('') + '</datalist>' +
    '<label>Folder<input name="path" placeholder="~/code/project" autocomplete="off" spellcheck="false"></label>' +
    '<div class="rmt-form-btns"><button type="submit" class="rmt-btn primary">Connect</button>' +
    (rmtList.length ? '<button type="button" class="rmt-btn" data-rmt-cancel>Cancel</button>' : '') + '</div>' +
    '<div class="rmt-note">Uses your SSH keys or agent, the same as <code>ssh</code> in a terminal. Files are read-only.</div>' +
    '</form>';
}

function rmtForm(show) {
  const f = rmtBody.querySelector('#rmt-form');
  if (!show) { f?.remove(); return; }
  if (f) { f.querySelector('input').focus(); return; }
  rmtBody.insertAdjacentHTML('afterbegin', rmtFormHTML());
  rmtBody.querySelector('#rmt-form input').focus();
}

async function rmtSubmit(e) {
  e.preventDefault();
  const f = e.target;
  const btn = f.querySelector('button[type=submit]');
  btn.disabled = true;
  btn.textContent = 'Connecting…';
  try {
    const j = await apiPostJson('/api/pxi/ssh/remotes', { host: f.host.value.trim(), path: f.path.value.trim() });
    rmtList = j.remotes || [];
    rmtErr.delete(j.remote.id);
    rmtOpen.add(j.remote.id + ':');
    rmtDraw();
  } catch (err) {
    btn.disabled = false;
    btn.textContent = 'Connect';
    showToast('!', err.message, 6000);
  }
}

function rmtDraw() {
  if (!rmtVisible) return;
  rmtBody.replaceChildren();
  if (!rmtList.length) {
    rmtBody.innerHTML = rmtFormHTML();
    return;
  }
  for (const r of rmtList) {
    const key = r.id + ':';
    const wrap = document.createElement('div');
    wrap.className = 'tw';
    const open = rmtOpen.has(key);
    wrap.innerHTML =
      '<div class="tr dir rmt-host' + (open ? ' open' : '') + '" data-rmt-id="' + esc(r.id) + '" data-rmt-dir="" title="' + esc(r.host + ':' + r.path) + '" style="padding-left:8px">' +
      '<span class="ar"></span><span class="nm">' + esc(r.host) + '</span><span class="rmt-path">' + esc(r.path) + '</span>' +
      '<button type="button" class="rmt-x" data-rmt-remove="' + esc(r.id) + '" title="Remove this remote">' + RMT_ICON.close + '</button></div>' +
      '<div class="kids' + (open ? ' open' : '') + '"></div>';
    rmtBody.append(wrap);
    if (open) rmtFill(r.id, '', wrap.querySelector('.kids'), 1);
  }
}

async function rmtFill(id, dir, kids, depth) {
  kids.innerHTML = '<div class="rmt-loading" style="padding-left:' + (8 + depth * 12 + 12) + 'px">Loading…</div>';
  let j;
  try {
    j = await api('/api/pxi/ssh/list', { id, dir });
  } catch (e) {
    rmtErr.set(id, e.message);
    kids.innerHTML = '<div class="rmt-error" style="padding-left:' + (8 + depth * 12) + 'px">' + esc(e.message) +
      ' <button type="button" class="rmt-btn" data-rmt-retry>Retry</button></div>';
    return;
  }
  rmtErr.delete(id);
  const pad = 8 + depth * 12;
  const rows = (j.entries || []).map(e => e.dir
    ? '<div class="tw"><div class="tr dir" data-rmt-id="' + esc(id) + '" data-rmt-dir="' + esc(e.path) + '" style="padding-left:' + pad + 'px" title="' + esc(e.path) + '">' +
      '<span class="ar"></span><span class="nm">' + esc(e.name) + '</span></div><div class="kids"></div></div>'
    : '<div class="tr file" data-rmt-id="' + esc(id) + '" data-rmt-file="' + esc(e.path) + '" style="padding-left:' + (pad + 12) + 'px" title="' + esc(e.path) + '">' +
      '<span class="ic" data-t="' + fileKind(e.name) + '"></span><span class="nm">' + esc(e.name) + '</span></div>');
  if (j.more) rows.push('<div class="rmt-note" style="padding-left:' + pad + 'px">More entries not shown</div>');
  if (!rows.length) rows.push('<div class="rmt-note" style="padding-left:' + (pad + 12) + 'px">Empty folder</div>');
  kids.innerHTML = rows.join('');
  // Folders that were open before a refresh open again.
  for (const row of kids.querySelectorAll(':scope > .tw > .tr.dir')) {
    if (rmtOpen.has(id + ':' + row.dataset.rmtDir)) {
      row.classList.add('open');
      const k = row.nextElementSibling;
      k.classList.add('open');
      rmtFill(id, row.dataset.rmtDir, k, depth + 1);
    }
  }
}

async function rmtClick(e) {
  if (e.target.closest('[data-rmt-cancel]')) { rmtForm(false); return; }
  const rm = e.target.closest('[data-rmt-remove]');
  if (rm) {
    e.stopPropagation();
    try {
      const j = await apiPostJson('/api/pxi/ssh/remotes', { remove: rm.dataset.rmtRemove });
      rmtList = j.remotes || [];
      rmtDraw();
    } catch (err) { showToast('!', err.message); }
    return;
  }
  if (e.target.closest('[data-rmt-retry]')) { rmtDraw(); return; }
  const file = e.target.closest('.tr.file[data-rmt-file]');
  if (file) {
    for (const s of rmtBody.querySelectorAll('.tr.sel')) s.classList.remove('sel');
    file.classList.add('sel');
    openFile('@ssh/' + file.dataset.rmtId + '/' + file.dataset.rmtFile);
    return;
  }
  const dir = e.target.closest('.tr.dir[data-rmt-id]');
  if (!dir) return;
  const id = dir.dataset.rmtId;
  const key = id + ':' + dir.dataset.rmtDir;
  const kids = dir.nextElementSibling;
  const open = !dir.classList.contains('open');
  dir.classList.toggle('open', open);
  kids.classList.toggle('open', open);
  if (open) {
    rmtOpen.add(key);
    const depth = dir.dataset.rmtDir ? dir.dataset.rmtDir.split('/').length + 1 : 1;
    rmtFill(id, dir.dataset.rmtDir, kids, depth);
  } else {
    rmtOpen.delete(key);
    kids.replaceChildren();
  }
}
