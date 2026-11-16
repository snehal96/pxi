// web/src/pxi-wake.js
// pxi: brings a workspace back after the Mac sleeps. A hidden page drops its
// event stream, and the workbench stops a workspace nobody has had open for 30
// minutes, so after a long sleep every request answers 503 and the page looked
// dead until pxi was restarted. When the page becomes visible again this checks
// once; if the workspace was stopped it asks the workbench to start it (the same
// GET a reload makes) and waits for it. With no unsaved edits the page then
// reloads to pick up fresh state; with unsaved edits it stays, and the stream
// reconnects on its own. No timers while the workspace is running.
import { S } from './state.js';
import { showToast } from './ui.js';

let wkeBusy = false;
let wkeLastCheck = 0;

const wkeUrl = p => new URL(p, document.baseURI);

async function wkeStatus() {
  try {
    const r = await fetch(wkeUrl('api/meta'), { cache: 'no-store', signal: AbortSignal.timeout(5000) });
    return r.status;
  } catch {
    return 0; // the workbench itself is unreachable; nothing this page can do
  }
}

async function wkeRecover() {
  showToast('↻', 'Restarting the workspace after sleep…', 15000);
  try {
    await fetch(wkeUrl(''), { cache: 'no-store' }); // the page GET starts the workspace
  } catch {
    return;
  }
  const deadline = Date.now() + 3 * 60 * 1000;
  while (Date.now() < deadline) {
    await new Promise(r => setTimeout(r, 700));
    if (await wkeStatus() === 200) break;
  }
  if (S.tabs.some(t => t.dirty)) {
    showToast('✓', 'Workspace restarted. Save your unsaved edits.', 6000);
    window.dispatchEvent(new Event('focus')); // reconnects the event stream now
  } else {
    location.reload();
  }
}

async function wkeCheck() {
  if (wkeBusy || document.visibilityState !== 'visible') return;
  const now = Date.now();
  if (now - wkeLastCheck < 5000) return;
  wkeLastCheck = now;
  wkeBusy = true;
  try {
    if (await wkeStatus() === 503) await wkeRecover();
  } finally {
    wkeBusy = false;
  }
}

export function initWake() {
  if (!location.pathname.includes('/w/')) return; // only workbench workspaces get stopped
  document.addEventListener('visibilitychange', wkeCheck);
  window.addEventListener('focus', wkeCheck);
}
