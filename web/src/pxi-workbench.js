// web/src/pxi-workbench.js
// pxi on the workbench page: in the Mac app, an "Open Folder…" button that
// shows the Finder folder picker (macos/Sources/BrowserWindow.swift
// chooseFolder). A browser cannot hand a page a folder's path, so there the
// path field and the Browse panel stay the way to pick one.

const pxiWbNative = window.webkit?.messageHandlers?.pxi;

function pxiWbPick() {
  pxiWbNative.postMessage({ type: 'openFolder' });
}

if (pxiWbNative) {
  const form = document.querySelector('#wb-open');
  if (form) {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.id = 'wb-pick';
    btn.className = 'wb-btn';
    btn.title = 'Choose a folder in Finder (⌘O)';
    btn.innerHTML = '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>Open Folder…';
    btn.addEventListener('click', pxiWbPick);
    form.append(btn);
    const keys = document.querySelector('.wb-keys');
    if (keys) keys.insertAdjacentHTML('beforeend', ' · <kbd>⌘O</kbd> choose in Finder');
  }
}
