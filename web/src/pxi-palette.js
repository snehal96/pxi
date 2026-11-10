// web/src/pxi-palette.js
// pxi: VS Code-style Quick Open on top of px0's palette (web/src/palette.js).
// px0 keeps the logic; this sets a placeholder per mode, moves a command's
// "(⌘K)" key out of its label into a right-aligned key cap, and empties the
// result list when the palette closes, so no rows stay in the page.
// Styles: web/pxi/shell.css. Top-level names are prefixed pal (Node fallback bundler).
import { $ } from './state.js';
import { COMMANDS } from './palette.js';

const PAL_PLACEHOLDER = {
  File: 'Search files by name (append : to go to line or @ to go to symbol)',
  Symbol: 'Go to symbol in the open file',
  Line: 'Type a line number to go to',
  Command: 'Type a command',
  Theme: 'Select color theme (arrows preview it)',
  'Open PR': 'Paste a pull request URL, then Enter',
};
const PAL_KEYS = /\s*\(([^()]{1,24})\)\s*$/;
const PAL_KEYLIKE = /^(?:[⌘⌥⇧⌃]|Ctrl\+|Alt\+|Shift\+|Cmd\+|F\d)/;

function palSync(box, input, mode) {
  const tag = mode.textContent.trim();
  box.dataset.pxiMode = tag.toLowerCase().replace(/\s+/g, '-');
  input.placeholder = PAL_PLACEHOLDER[tag] || '';
}

function palKeys(list, box) {
  if (box.dataset.pxiMode !== 'command') return;
  for (const name of list.querySelectorAll('.pi .pn')) {
    if (name.dataset.pxiKeys) continue;
    name.dataset.pxiKeys = '1';
    const m = name.textContent.match(PAL_KEYS);
    if (!m || !PAL_KEYLIKE.test(m[1].trim())) continue;
    name.textContent = name.textContent.slice(0, m.index);
    const kbd = document.createElement('span');
    kbd.className = 'pr pxi-kbd';
    kbd.textContent = m[1].trim();
    name.parentElement.append(kbd);
  }
}

// Reload Window reloads the page (keeps the server and its terminals); Restart,
// in the Mac app only, restarts the app and its server and reopens the windows.
function palAddWindowCommands() {
  const native = window.webkit?.messageHandlers?.pxi;
  COMMANDS.push({ name: 'Developer: Reload Window' + (native ? ' (⌥⌘R)' : ''), run: () => location.reload() });
  if (native) COMMANDS.push({ name: 'Developer: Restart pxi (⇧⌥⌘R)', run: () => native.postMessage({ type: 'restart' }) });
}

export function initPxiPalette() {
  palAddWindowCommands();
  const overlay = $('#overlay');
  const box = $('#palette');
  const input = $('#pal');
  const list = $('#pal-list');
  const mode = $('#pal-mode');
  if (!overlay || !box || !input || !list || !mode) return;
  document.body.classList.add('pxi-palette');
  new MutationObserver(() => palSync(box, input, mode)).observe(mode, { childList: true, characterData: true, subtree: true });
  new MutationObserver(() => palKeys(list, box)).observe(list, { childList: true });
  new MutationObserver(() => {
    if (overlay.hidden) list.replaceChildren(); // closed: keep no rows
  }).observe(overlay, { attributes: true, attributeFilter: ['hidden'] });
  palSync(box, input, mode);
}
