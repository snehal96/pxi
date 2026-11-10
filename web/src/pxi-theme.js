// web/src/pxi-theme.js
// pxi: a theme that follows macOS light/dark (px0 #224). Turning it on keeps the
// current theme for its own scheme and remembers one theme per scheme; picking a
// theme by hand turns it off, as in VS Code. In the Mac app the window drops its
// fixed appearance while this is on (pxi-native.js posts scheme "auto"), so the
// page's prefers-color-scheme reports the system and not the theme.
// Top-level names are prefixed thm (Node fallback bundler).
import { showToast } from './ui.js';
import { listThemes, currentTheme, setTheme } from './theme.js';
import { COMMANDS } from './palette.js';

const THM_KEY = 'pxi.theme.auto';
const THM_ATTR = 'data-pxi-theme-auto';
const THM_DEFAULT = { dark: 'pxi-dark', light: 'pxi-light' };
const thmQuery = window.matchMedia?.('(prefers-color-scheme: dark)');
let thmPair = null; // {dark, light} while on
let thmApplying = false;

function thmRead() {
  try { const p = JSON.parse(localStorage.getItem(THM_KEY)); return p && typeof p === 'object' ? p : null; } catch { return null; }
}

function thmScheme(id) {
  return (listThemes().find(t => t.id === id)?.scheme || '').includes('light') ? 'light' : 'dark';
}

function thmApply() {
  if (!thmPair) return;
  const want = thmQuery?.matches === false ? 'light' : 'dark';
  thmApplying = true;
  try {
    if (!setTheme(thmPair[want], false)) setTheme(THM_DEFAULT[want], false);
  } finally { thmApplying = false; }
}

function thmSet(on) {
  if (on) {
    const prev = thmRead() || {};
    const cur = currentTheme();
    thmPair = { ...THM_DEFAULT, ...(prev.dark || prev.light ? prev : {}), [thmScheme(cur)]: cur };
    try { localStorage.setItem(THM_KEY, JSON.stringify(thmPair)); } catch {}
  } else {
    thmPair = null;
    try { localStorage.removeItem(THM_KEY); } catch {}
  }
  document.documentElement.toggleAttribute(THM_ATTR, on);
  thmApply();
}

// theme.js setTheme(): a theme chosen by hand (persisted) ends auto.
export function pxiThemeChosen() {
  if (thmPair && !thmApplying) thmSet(false);
}

// theme.js initTheme(): true when auto is on and has set the theme.
export function pxiThemeInit() {
  const p = thmRead();
  if (!p) return false;
  thmPair = { ...THM_DEFAULT, ...p };
  document.documentElement.setAttribute(THM_ATTR, '');
  thmApply();
  return true;
}

export function initPxiTheme() {
  thmQuery?.addEventListener('change', thmApply);
  COMMANDS.push({
    name: 'Preferences: Theme Follows macOS Light/Dark',
    run: () => {
      thmSet(!thmPair);
      if (!thmPair) setTheme(currentTheme()); // keep what is showing after a reload
      const name = listThemes().find(t => t.id === currentTheme())?.name || currentTheme();
      showToast('Theme', thmPair ? 'Follows macOS (now ' + name + ')' : 'Fixed: ' + name);
    },
  });
}
