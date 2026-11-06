// web/src/pxi-html.js
// pxi: preview .html/.htm files in the editor, like the Markdown preview. An
// eye icon in the editor toolbar (or ⌥M) switches between the source and the
// rendered page; the last choice is remembered.
//
// The page is shown in an iframe from /api/pxi/html/<path> (fork_html.go),
// sandboxed twice: the iframe has no allow-scripts / allow-same-origin, and the
// server sends a CSP sandbox. It cannot run scripts, read pxi's API, or load
// anything from outside the workspace.
//
// Memory rule: nothing is built until a preview is shown, and the iframe (with
// its document) is removed, not hidden, on Source, tab switch or tab close.
// Top-level names are prefixed html: the Node fallback bundler shares one scope.
import { $, doc_ } from './state.js';
import { on } from './bus.js';

const HTML_PREF_KEY = 'pxi.htmlPreview';
const HTML_EXT = /\.html?$/i;
const HTML_EYE = '<svg viewBox="0 0 16 16" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"><path d="M1.5 8S4 3.5 8 3.5 14.5 8 14.5 8 12 12.5 8 12.5 1.5 8 1.5 8z"/><circle cx="8" cy="8" r="2"/></svg>';

let htmlBtn = null;
let htmlFrame = null;
let htmlShownPath = '';

function htmlPref() {
  try { return localStorage.getItem(HTML_PREF_KEY) === 'true'; } catch { return false; }
}

function htmlSetPref(on) {
  try { localStorage.setItem(HTML_PREF_KEY, on ? 'true' : 'false'); } catch {}
}

function htmlTab(d = doc_()) {
  return d && !d.isImage && !d.deleted && HTML_EXT.test(d.path || '') ? d : null;
}

function htmlUrl(path) {
  return 'api/pxi/html/' + path.split('/').map(encodeURIComponent).join('/');
}

function htmlRemove() {
  if (!htmlFrame) return;
  htmlFrame.remove(); // its document goes with it
  htmlFrame = null;
  htmlShownPath = '';
  document.body.classList.remove('pxi-html-preview');
}

/** Shows or removes the preview to match the active tab. */
function htmlSync() {
  const d = htmlTab();
  if (htmlBtn) {
    // Only write what changed: these attributes are what the observer
    // watches, so rewriting them would call htmlSync again forever.
    if (htmlBtn.hidden !== !d) htmlBtn.hidden = !d;
    const lit = !!d && htmlPref() && !d.diffMode && !d.isEditing;
    if (htmlBtn.classList.contains('on') !== lit) htmlBtn.classList.toggle('on', lit);
  }
  const want = d && htmlPref() && !d.diffMode && !d.isEditing ? d.path : '';
  if (want === htmlShownPath) return;
  htmlRemove();
  if (!want) return;
  const editor = $('#editor');
  if (!editor) return;
  htmlFrame = document.createElement('iframe');
  htmlFrame.id = 'pxi-html-frame';
  htmlFrame.setAttribute('sandbox', ''); // no scripts, opaque origin
  htmlFrame.setAttribute('referrerpolicy', 'no-referrer');
  htmlFrame.title = 'Preview of ' + want;
  htmlFrame.src = htmlUrl(want);
  editor.append(htmlFrame);
  htmlShownPath = want;
  document.body.classList.add('pxi-html-preview');
}

function htmlToggle() {
  const d = htmlTab();
  if (!d) return false;
  htmlSetPref(!(htmlPref() && htmlShownPath === d.path));
  htmlSync();
  return true;
}

export function initHtmlPreview() {
  const switches = $('#view-switches');
  if (switches) {
    htmlBtn = document.createElement('button');
    htmlBtn.id = 'pxi-html-btn';
    htmlBtn.type = 'button';
    htmlBtn.hidden = true;
    htmlBtn.title = 'Preview HTML (⌥M)';
    htmlBtn.setAttribute('aria-label', 'Preview HTML');
    htmlBtn.innerHTML = HTML_EYE;
    htmlBtn.addEventListener('mousedown', e => e.preventDefault());
    htmlBtn.addEventListener('click', htmlToggle);
    switches.prepend(htmlBtn);
    // Diff mode and edit mode take the editor over: px0 updates these
    // controls whenever a tab's view changes, which is when to re-check.
    new MutationObserver(htmlSync).observe(switches, { subtree: true, attributes: true, attributeFilter: ['class', 'hidden'] });
  }
  on('tab:activated', htmlSync);
  on('tab:closed', htmlSync);
  on('tabs:cleared', htmlSync);
  // ⌥M toggles the Markdown preview in px0; do the same for HTML tabs.
  addEventListener('keydown', e => {
    if (!e.altKey || e.metaKey || e.ctrlKey || e.code !== 'KeyM') return;
    if (htmlTab() && htmlToggle()) { e.preventDefault(); e.stopPropagation(); }
  }, true);
}
