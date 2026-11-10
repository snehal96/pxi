// web/src/pxi-checkpoint.js
// pxi: review checkpoint (px0 #90). "Review: Set Checkpoint" marks now; from
// then on the explorer puts a dot on every file added or changed since, and a
// status bar item counts them (click: open the next one). The server keeps one
// git tree hash (fork_checkpoint.go). Nothing runs while no checkpoint is set:
// no listener work, no observer, no status item.
// Styles: web/pxi/shell.css. Top-level names are prefixed ckp (Node fallback bundler).
import { $, api, apiPostJson } from './state.js';
import { showToast } from './ui.js';
import { COMMANDS } from './palette.js';
import { openFile } from './tabs.js';
import { onStream } from './stream.js';

let ckpInfo = null;      // {at, files: {path: A|M|D}, count} while set
let ckpDirs = null;      // folders holding a changed file
let ckpObserver = null;
let ckpOffStream = null;
let ckpTimer = 0;
let ckpNext = 0;

function ckpSetInfo(j) {
  ckpInfo = j && j.active ? j : null;
  ckpDirs = null;
  if (ckpInfo) {
    ckpDirs = new Set();
    for (const p of Object.keys(ckpInfo.files || {})) {
      for (let i = p.lastIndexOf('/'); i > 0; i = p.lastIndexOf('/', i - 1)) ckpDirs.add(p.slice(0, i));
    }
  }
  ckpWatch(!!ckpInfo);
  ckpPaint();
  ckpStatus();
}

function ckpPaint() {
  const tree = $('#tree');
  if (!tree) return;
  const files = ckpInfo?.files || {};
  for (const row of tree.querySelectorAll('.tr.file[data-file]')) {
    const st = files[row.dataset.file];
    if (st) row.dataset.ck = st; else if (row.dataset.ck) delete row.dataset.ck;
  }
  for (const row of tree.querySelectorAll('.tr.dir[data-dir]')) {
    row.classList.toggle('pxi-ck-dir', !!ckpDirs?.has(row.dataset.dir));
  }
}

function ckpTime() {
  const d = new Date(ckpInfo.at);
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

function ckpStatus() {
  let btn = $('#pxi-ck-btn');
  if (!ckpInfo) { btn?.remove(); return; }
  if (!btn) {
    btn = document.createElement('button');
    btn.id = 'pxi-ck-btn';
    btn.className = 'footer-btn';
    btn.addEventListener('click', ckpOpenNext);
    const after = $('#pxi-branch-btn');
    if (after) after.after(btn); else $('#status')?.prepend(btn);
  }
  const n = ckpInfo.count || 0;
  btn.innerHTML = '<span class="pxi-ck-dot"></span><span>' + n + ' since ' + ckpTime() + '</span>';
  const names = Object.keys(ckpInfo.files || {}).slice(0, 15);
  btn.title = 'Review checkpoint set at ' + ckpTime() + ': ' + n + (n === 1 ? ' file' : ' files') + ' changed since' +
    (names.length ? '\n' + names.join('\n') + (n > names.length ? '\n…' : '') + '\nClick: open the next one' : '') +
    '\n(Command palette: Review: Clear Checkpoint)';
}

function ckpOpenNext() {
  const paths = Object.entries(ckpInfo?.files || {}).filter(([, st]) => st !== 'D').map(([p]) => p).sort();
  if (!paths.length) { showToast('Checkpoint', 'Nothing has changed since ' + (ckpInfo ? ckpTime() : 'it was set')); return; }
  openFile(paths[ckpNext++ % paths.length]);
}

async function ckpRefresh() {
  try { ckpSetInfo(await api('/api/pxi/checkpoint')); } catch {}
}

function ckpSoon() {
  clearTimeout(ckpTimer);
  ckpTimer = setTimeout(ckpRefresh, 700);
}

// Only while a checkpoint is set: refresh on git changes, repaint rows the tree draws.
function ckpWatch(on) {
  if (on && !ckpObserver) {
    ckpOffStream = onStream('git-status', ckpSoon);
    const tree = $('#tree');
    if (tree) {
      let queued = false;
      ckpObserver = new MutationObserver(() => {
        if (queued) return;
        queued = true;
        requestAnimationFrame(() => { queued = false; ckpPaint(); });
      });
      ckpObserver.observe(tree, { childList: true, subtree: true });
    }
  } else if (!on && ckpObserver) {
    ckpObserver.disconnect();
    ckpObserver = null;
    ckpOffStream?.();
    ckpOffStream = null;
    clearTimeout(ckpTimer);
  }
}

async function ckpMark(clear) {
  try {
    ckpNext = 0;
    ckpSetInfo(await apiPostJson('/api/pxi/checkpoint', { clear }));
    showToast('Checkpoint', clear ? 'Cleared' : 'Set. Files that change from now on get a dot');
  } catch (e) {
    showToast('!', (clear ? 'Could not clear the checkpoint: ' : 'Could not set a checkpoint: ') + e.message);
  }
}

export function initPxiCheckpoint() {
  COMMANDS.push(
    { name: 'Review: Set Checkpoint (flag files that change from now on)', run: () => ckpMark(false) },
    { name: 'Review: Clear Checkpoint', run: () => ckpMark(true) },
  );
  ckpRefresh(); // a checkpoint set before a reload is still on the server
}
