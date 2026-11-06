// web/src/pxi-native.js
// pxi: the bridge to the Mac app's native title bar (macos/Sources): its back
// and forward buttons walk px0's navigation history, its search box opens
// quick open, and it takes the theme's colours. Only active inside the app;
// adds no DOM.
import { S } from './state.js';
import { go } from './history.js';
import { openPalette } from './palette.js';
import { openClaude } from './pxi-claude.js';

export function initNative() {
  const bridge = window.webkit?.messageHandlers?.pxi;
  if (!bridge) return; // a browser, not the Mac app
  window.pxiNative = {
    back: () => go(-1),
    forward: () => go(1),
    quickOpen: () => openPalette('file'),
    commands: () => openPalette('command'),
    // A notification was clicked.
    openClaude: (session, where) => openClaude({ session, where }),
  };
  const nativePost = () => {
    const cs = getComputedStyle(document.body);
    const v = n => cs.getPropertyValue(n).trim();
    bridge.postMessage({
      type: 'theme',
      bar: v('--pxi-activity-bg') || v('--bg2'),
      fg: v('--fg'),
      dim: v('--dim'),
      field: v('--pxi-input-bg') || v('--bg'),
      scheme: getComputedStyle(document.documentElement).colorScheme || 'dark',
      name: S.meta?.name || '',
    });
  };
  nativePost();
  new MutationObserver(nativePost).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
}
